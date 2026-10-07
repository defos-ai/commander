package runner

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
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
		if !operation.Status.Terminal() || slices.ContainsFunc(operation.Tasks, operations.Task.NeedsRecovery) || pendingQuestionDeadline(operation) {
			r.states[operation.ID] = &operationState{operation: operation.Clone(), changed: make(chan struct{})}
		}
	}
	return nil
}

func (r *Runner) resumeTasks() {
	for _, state := range r.states {
		for _, task := range state.operation.Clone().Tasks {
			if task.NeedsRecovery() {
				allowDispatch := !state.operation.Status.Terminal() && task.DeadlineAt.After(time.Now())
				r.workers++
				go r.recoverTask(task, allowDispatch)
			}
		}
	}
}

func (r *Runner) recoverTask(task operations.Task, allowDispatch bool) {
	deadline := *task.DeadlineAt
	if task.Status == operations.TaskInterrupted || !deadline.After(time.Now()) {
		// Even an interrupted or expired task may have a durable result.
		// Allow one bounded reconciliation window to retrieve its saved outcome.
		deadline = time.Now().Add(5 * time.Second)
	}
	ctx, cancel := context.WithDeadline(r.ctx, deadline)
	defer cancel()
	call := actionmodel.ActionCall{ActionName: task.ActionName, Input: task.Input,
		Work: messages.WorkContext{OperationID: task.OperationID, TaskID: task.ID, StepID: task.StepID}}
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
