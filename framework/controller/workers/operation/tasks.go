package operation

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
)

// Assign returns immediately after recording the task. Work belongs to the
// operation worker lifetime, so ending an MCP command does not interrupt accepted work.
func (r *Worker) Assign(ctx context.Context, command commands.AssignTaskCommand) (operations.Task, error) {
	if err := command.Validate(); err != nil {
		return operations.Task{}, err
	}
	task := queuedTask(command.OperationID, "", actionmodel.NextAction{ActionName: command.ActionName, Input: command.Input})
	return submit(ctx, r, func(ctx context.Context) (operations.Task, error) {
		s, err := r.activeState(ctx, command.OperationID)
		if err != nil {
			return operations.Task{}, err
		}
		source, err := assignmentSessionSource(s.operation, command.SessionTaskID)
		if err != nil {
			return operations.Task{}, err
		}
		records, err := r.catalog.Agents(ctx)
		if err != nil {
			return operations.Task{}, err
		}

		resolved, err := r.normalize(ctx, s.operation, source, []actionmodel.NextAction{{ActionName: task.ActionName, Input: task.Input}}, records)
		if err != nil {
			return operations.Task{}, err
		}
		normalized := resolved[0]
		if source != nil && normalized.SessionIntent == nil {
			return operations.Task{}, fmt.Errorf("%w: session_task_id requires a session-aware action", domain.ErrInvalidInput)
		}
		task.Input = normalized.Input
		task.SessionIntent = normalized.SessionIntent.Clone()
		if task.SessionIntent != nil && task.SessionIntent.Mode == "open" {
			if task.SessionIntent.ScopeID == "" {
				task.SessionIntent.ScopeID = task.CommandID
			}
			if task.SessionIntent.ID == "" {
				task.SessionIntent.ID = task.CommandID
			}
		}
		if r.sessions != nil {
			if err := r.sessions.CheckAdmission(ctx, task); err != nil {
				return operations.Task{}, fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
			}
		} else if task.SessionIntent != nil {
			return operations.Task{}, errors.New("runtime session persistence is not configured")
		}

		next, err := r.store.QueueTask(ctx, command.OperationID, task)
		if err := r.acceptUpdate(s, next, err); err != nil {
			return operations.Task{}, err
		}
		r.observeAccepted(ctx, s)
		r.signalScheduler()
		return s.operation.Clone().Tasks[taskIndex(&s.operation, task.ID)], nil
	})
}

func assignmentSessionSource(operation operations.Operation, taskID string) (*operations.Task, error) {
	if taskID == "" {
		return nil, nil
	}
	index := taskIndex(&operation, taskID)
	if index < 0 {
		return nil, operations.ErrUnknownTask
	}
	source := operation.Tasks[index]
	switch source.Status {
	case operations.TaskSucceeded, operations.TaskFailed, operations.TaskCancelled:
	default:
		return nil, fmt.Errorf("%w: session_task_id must reference a finished task with a known outcome", domain.ErrInvalidInput)
	}
	if source.SessionIntent == nil {
		return nil, fmt.Errorf("%w: session_task_id must reference a session-aware task", domain.ErrInvalidInput)
	}
	if err := source.SessionIntent.Validate(); err != nil {
		return nil, fmt.Errorf("%w: session_task_id references an invalid session: %w", domain.ErrInvalidInput, err)
	}
	for _, question := range operation.Questions {
		if question.TaskID == source.ID && question.Pending() {
			return nil, operations.ErrQuestionPending
		}
	}
	return &source, nil
}

func (r *Worker) runTask(task operations.Task, call actionmodel.ActionCall) {
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

func (r *Worker) finishTask(s *operationState, task operations.Task, result actionmodel.ActionOutput, runErr error) {
	if s.err != nil {
		return
	}
	// Stopping the host suspends observation, not the remote action. Leave the
	// persisted running record for the next operation worker to reconcile.
	if runErr != nil && r.ctx.Err() != nil {
		return
	}
	if runErr == nil && len(result.NextActions) != 0 && !s.operation.Status.Terminal() {
		ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
		records, err := r.catalog.Agents(ctx)
		var resolved []actionmodel.NextAction
		if err == nil {
			resolved, err = r.normalize(ctx, s.operation, &task, result.NextActions, records)
		}
		cancel()
		if err != nil {
			// Keep the durable execution outcome recoverable until its proposals
			// can be classified. A lookup outage is not an invalid action contract.
			if r.pendingResults == nil {
				r.pendingResults = make(map[string]taskResult)
			}
			if _, pending := r.pendingResults[task.ID]; !pending {
				r.logger.Warn("Task result awaiting admission state", "operation_id", task.OperationID, "task_id", task.ID, "error_type", fmt.Sprintf("%T", err))
			}
			r.pendingResults[task.ID] = taskResult{task: task.Clone(), output: result.Clone()}
			return
		}
		result.NextActions = resolved
	}
	delete(r.pendingResults, task.ID)
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
	if err := r.acceptUpdate(s, next, err); err != nil {
		s.err = errors.Join(s.err, fmt.Errorf("save task result: %w", err))
		close(s.changed)
		s.changed = make(chan struct{})
		return
	}
	r.observeAccepted(saveCtx, s)
}

func (r *Worker) retryResults() {
	for _, result := range r.pendingResults {
		r.finishTask(r.states[result.task.OperationID], result.task, result.output, result.err)
	}
}
