package runner

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

// recover restores state before the runner becomes available to callers. It
// never dispatches commands. Stored results and questions remain authoritative.
func (r *Runner) recover(ctx context.Context) error {
	records, err := r.store.List(ctx)
	if err != nil {
		return fmt.Errorf("load operations for recovery: %w", err)
	}
	for _, record := range records {
		operation, err := r.store.Recover(ctx, record.ID, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("recover operation %s: %w", record.ID, err)
		}
		if r.sessions != nil {
			if err := r.sessions.Observe(ctx, runtimeSessions.Evidence{OperationID: operation.ID}); err != nil {
				return err
			}
		}
		if !operation.Status.Terminal() || slices.ContainsFunc(operation.Tasks, operations.Task.NeedsRecovery) || pendingQuestionDeadline(operation) {
			r.states[operation.ID] = &operationState{operation: operation.Clone(), changed: make(chan struct{})}
		}
	}
	if r.sessions != nil {
		records, err := r.catalog.Agents(ctx)
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := r.sessions.Observe(ctx, runtimeSessions.Evidence{AgentID: record.ID}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runner) resumeTasks() {
	for _, state := range r.states {
		for _, task := range state.operation.Clone().Tasks {
			if task.Status == operations.TaskRunning {
				allowDispatch := !state.operation.Status.Terminal() && task.DeadlineAt.After(time.Now())
				r.workers++
				go r.recoverTask(task, allowDispatch)
			}
		}
	}
	r.observeInterruptedTasks()
}

func (r *Runner) recoverTask(task operations.Task, allowDispatch bool) {
	deadline := *task.DeadlineAt
	if !deadline.After(time.Now()) {
		// An expired running task may already have a durable result. Bound this
		// startup check; inconclusive outcomes move to periodic observation.
		deadline = time.Now().Add(taskObservationTimeout)
	}
	ctx, cancel := context.WithDeadline(r.ctx, deadline)
	defer cancel()
	call := actionmodel.ActionCall{ActionName: task.ActionName, Input: task.Input,
		Work: messages.WorkContext{OperationID: task.OperationID, TaskID: task.ID, StepID: task.StepID}}
	if allowDispatch && r.sessions != nil {
		prepared, err := r.sessions.Prepare(ctx, runtimeSessions.Assignment{OperationID: task.OperationID, TaskID: task.ID, Candidate: task.AgentID, Timeout: r.taskTimeout})
		if err != nil {
			allowDispatch = false
		} else {
			call.Session = prepared.Binding
		}
	}
	var result actionmodel.ActionOutput
	var err error
	if task.Status == operations.TaskRunning && allowDispatch {
		if dispatcher, ok := r.executor.(DurableActionExecutor); ok {
			result, err = dispatcher.DispatchAction(ctx, task.AgentID, call)
		} else {
			result, err = r.executor.RecoverAction(ctx, task.AgentID, call)
		}
	} else {
		result, err = r.executor.RecoverAction(ctx, task.AgentID, call)
	}
	r.results <- taskResult{task: task, output: result, err: err}
}
