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
		s := &operationState{operation: operation.Clone(), changed: make(chan struct{})}
		next := operation.Clone()
		expired := expireQuestions(&next, time.Now().UTC())
		if operation.Status.Terminal() && !slices.ContainsFunc(operation.Tasks, needsTaskRecovery) {
			if expired {
				if err := r.update(ctx, s, func(next *operations.Operation) error {
					expireQuestions(next, time.Now().UTC())
					return nil
				}); err != nil {
					return fmt.Errorf("persist expired questions for %s: %w", operation.ID, err)
				}
			}
			if pendingQuestionDeadline(next) {
				r.states[operation.ID] = s
			}
			continue
		}
		if err := r.update(ctx, s, func(next *operations.Operation) error {
			expireQuestions(next, time.Now().UTC())
			refreshStatus(next)
			appendEvent(next, operations.OperationRecovered, "", "", "Operation restored; existing commands are observed without being dispatched again")
			return nil
		}); err != nil {
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
	for _, state := range r.states {
		for _, task := range state.operation.Clone().Tasks {
			if needsTaskRecovery(task) {
				allowDispatch := !state.operation.Status.Terminal() && task.DeadlineAt.After(time.Now())
				r.workers++
				go r.recoverTask(task, allowDispatch)
			}
		}
	}
}

func needsTaskRecovery(task operations.Task) bool {
	return task.Status == operations.TaskRunning || task.Status == operations.TaskInterrupted
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
