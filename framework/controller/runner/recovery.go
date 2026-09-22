package runner

import (
	"context"
	"fmt"
	"slices"
	"time"

	"commander/framework/artifact"
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/operations"
	"commander/framework/id"
)

// recover restores state before the runner becomes available to callers. It
// never dispatches commands. Stored results and questions remain authoritative.
func (r *Runner) recover(ctx context.Context, artifacts ArtifactRestorer) error {
	records, err := r.store.List(ctx)
	if err != nil {
		return fmt.Errorf("load operations for recovery: %w", err)
	}
	var refs []artifact.Ref
	for _, operation := range records {
		if err := validateRecoveredOperation(operation); err != nil {
			return fmt.Errorf("recover operation %s: %w", operation.ID, err)
		}
		for _, task := range operation.Tasks {
			if task.Result == nil {
				continue
			}
			refs = append(refs, task.Result.Artifacts...)
			if task.Result.OutputArtifact != nil {
				refs = append(refs, *task.Result.OutputArtifact)
			}
		}
		if operation.Status.Terminal() && !slices.ContainsFunc(operation.Tasks, needsTaskRecovery) {
			continue
		}
		s := &operationState{operation: operation.Clone(), changed: make(chan struct{})}
		next := operation.Clone()
		refreshStatus(&next)
		appendEvent(&next, operations.OperationRecovered, "", "", "Operation restored; existing commands are observed without being dispatched again")
		if err := r.save(ctx, s, next); err != nil {
			return fmt.Errorf("persist recovered operation %s: %w", operation.ID, err)
		}
		r.states[operation.ID] = s
	}
	if err := artifacts.RestoreArtifacts(refs); err != nil {
		return fmt.Errorf("restore operation artifacts: %w", err)
	}
	return nil
}

func (r *Runner) resumeTasks() {
	// Collect before launching: a recovered result can update its operation as
	// soon as its goroutine starts.
	type resumedTask struct {
		state *operationState
		task  operations.Task
	}
	var pending []resumedTask
	for _, state := range r.states {
		for _, task := range state.operation.Clone().Tasks {
			if needsTaskRecovery(task) {
				pending = append(pending, resumedTask{state, task})
			}
		}
	}
	for _, pending := range pending {
		r.wg.Add(1)
		go r.recoverTask(pending.state, pending.task)
	}
}

func needsTaskRecovery(task operations.Task) bool {
	return task.Status == operations.TaskRunning || task.Status == operations.TaskInterrupted
}

func (r *Runner) recoverTask(state *operationState, task operations.Task) {
	defer r.wg.Done()
	defer r.signalScheduler()
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
	state.mu.Lock()
	allowDispatch := !state.operation.Status.Terminal() && task.DeadlineAt.After(time.Now())
	state.mu.Unlock()
	if task.Status == operations.TaskRunning && allowDispatch {
		if dispatcher, ok := r.executor.(DurableActionExecutor); ok {
			result, err = dispatcher.DispatchAction(ctx, task.AgentID, call)
		} else {
			result, err = r.executor.RecoverAction(ctx, task.AgentID, call)
		}
	} else {
		result, err = r.executor.RecoverAction(ctx, task.AgentID, call)
	}
	r.finishTask(state, task, result, err)
}

func validateRecoveredOperation(operation operations.Operation) error {
	switch operation.Status {
	case operations.Planning, operations.Running, operations.Waiting, operations.Completed, operations.Failed, operations.Cancelled:
	default:
		return fmt.Errorf("invalid operation status %q", operation.Status)
	}
	seen := make(map[string]bool)
	for _, task := range operation.Tasks {
		undispatched := task.Status == operations.TaskQueued || task.Status == operations.TaskCancelled && task.DeadlineAt == nil
		if !id.Valid(task.ID) || (!undispatched || task.AgentID != "") && !id.ValidAgent(task.AgentID) || task.OperationID != operation.ID || seen[task.ID] {
			return fmt.Errorf("invalid or duplicate task identity %q", task.ID)
		}
		seen[task.ID] = true
		if task.CommandID != task.ID {
			return fmt.Errorf("task %s has a mismatched command identity", task.ID)
		}
		if task.Status == operations.TaskQueued || task.Status == operations.TaskCancelled && task.DeadlineAt == nil {
			continue
		}
		if task.DeadlineAt == nil || task.DeadlineAt.IsZero() {
			return fmt.Errorf("task %s has no execution deadline", task.ID)
		}
		switch task.Status {
		case operations.TaskRunning, operations.TaskSucceeded, operations.TaskFailed, operations.TaskCancelled, operations.TaskInterrupted:
		default:
			return fmt.Errorf("task %s has an invalid status", task.ID)
		}
		if task.Result != nil {
			if task.Result.ActionName != task.ActionName {
				return fmt.Errorf("task %s result belongs to another action", task.ID)
			}
			if err := (operations.AgentObservation{AgentID: task.AgentID, Result: *task.Result}).Validate(); err != nil {
				return fmt.Errorf("task %s result: %w", task.ID, err)
			}
		}
	}
	return nil
}
