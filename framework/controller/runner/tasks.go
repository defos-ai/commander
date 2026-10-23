package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

// Assign returns immediately after recording the task. Work belongs to the
// runner lifetime, so ending an MCP command does not interrupt accepted work.
func (r *Runner) Assign(ctx context.Context, command commands.AssignTaskCommand) (operations.Task, error) {
	if err := command.Validate(); err != nil {
		return operations.Task{}, err
	}
	task := queuedTask(command.OperationID, command.StepID, "", actionmodel.NextAction{ActionName: command.ActionName, Input: command.Input})
	return submit(ctx, r, func(ctx context.Context) (operations.Task, error) {
		s, err := r.activeState(ctx, command.OperationID)
		if err != nil {
			return operations.Task{}, err
		}
		records, err := r.catalog.Agents(ctx)
		if err != nil {
			return operations.Task{}, err
		}

		resolved, err := r.normalize(ctx, s.operation, []actionmodel.NextAction{{ActionName: task.ActionName, Input: task.Input}}, records)
		if err != nil {
			return operations.Task{}, err
		}
		normalized := resolved[0]
		task.SessionIntent = normalized.SessionIntent.Clone()
		if task.SessionIntent != nil && task.SessionIntent.Mode == "create" {
			task.SessionIntent.ID = task.ID
		}
		if r.sessions != nil {
			if err := r.sessions.CheckAdmission(ctx, task); err != nil {
				return operations.Task{}, fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
			}
		} else if task.SessionIntent != nil {
			return operations.Task{}, errors.New("runtime session persistence is not configured")
		}

		next, err := r.store.QueueTask(ctx, command.OperationID, task)
		if err := s.acceptUpdate(next, err); err != nil {
			return operations.Task{}, err
		}
		if r.sessions != nil {
			if err := r.sessions.Observe(ctx, runtimeSessions.Evidence{OperationID: command.OperationID}); err != nil {
				return operations.Task{}, err
			}
		}
		r.signalScheduler()
		return s.operation.Clone().Tasks[taskIndex(&s.operation, task.ID)], nil
	})
}

func (r *Runner) runTask(task operations.Task, call actionmodel.ActionCall) {
	ctx, cancel := context.WithDeadline(r.ctx, *task.DeadlineAt)
	defer cancel()
	var result actionmodel.ActionOutput
	var runErr error
	if dispatcher, ok := r.executor.(DurableActionExecutor); ok {
		result, runErr = dispatcher.DispatchAction(ctx, task.AgentID, call)
	} else {
		result, runErr = r.executor.RunAction(ctx, task.AgentID, call)
	}
	r.results <- taskResult{task: task, output: result, err: runErr}
}

func (r *Runner) finishTask(s *operationState, task operations.Task, result actionmodel.ActionOutput, runErr error) {
	// Stopping the host suspends observation, not the remote action. Leave the
	// persisted running record for the next runner to reconcile.
	if runErr != nil && r.ctx.Err() != nil {
		return
	}
	if runErr == nil {
		records, err := r.catalog.Agents(r.ctx)
		if err != nil {
			records = nil
		}
		resolved, resolveErr := r.normalize(r.ctx, s.operation, result.NextActions, records)
		if resolveErr != nil {
			// Keep the accepted outcome and proposals, with explicit admission
			// blockers when their contracts cannot be resolved durably.
			resolved = runtimeSessions.Normalize(result.NextActions, nil)
		}
		result.NextActions = resolved
	}
	outcome := operations.TaskResult{TaskID: task.ID, AgentID: task.AgentID, Status: operations.TaskSucceeded, Result: &result, FinishedAt: time.Now().UTC()}
	if runErr != nil {
		outcome.Status, outcome.Result, outcome.Error = operations.TaskFailed, nil, runErr.Error()
		if errors.Is(runErr, executions.ErrIndeterminate) || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled) {
			outcome.Status = operations.TaskInterrupted
		}
	}
	// Preserve evidence and errors during shutdown, after the task context ends.
	saveCtx, done := context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
	defer done()
	next, err := r.store.FinishTask(saveCtx, s.operation.ID, outcome)
	if err == nil && r.sessions != nil {
		err = r.sessions.Observe(saveCtx, runtimeSessions.Evidence{OperationID: s.operation.ID})
	}
	if err := s.acceptUpdate(next, err); err != nil {
		s.err = errors.Join(s.err, fmt.Errorf("save task result: %w", err))
		close(s.changed)
		s.changed = make(chan struct{})
	}
}
