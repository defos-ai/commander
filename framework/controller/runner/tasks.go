package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"commander/framework/comms/clients/controller/commands"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/executions"
	"commander/framework/domain/operations"
)

// Assign returns immediately after recording the task. Work belongs to the
// runner lifetime, so ending an MCP command does not interrupt accepted work.
func (r *Runner) Assign(ctx context.Context, command commands.AssignTaskCommand) (operations.Task, error) {
	if err := ctx.Err(); err != nil {
		return operations.Task{}, err
	}
	ctx, cancel := r.requestContext(ctx)
	defer cancel()
	if err := command.Validate(); err != nil {
		return operations.Task{}, err
	}
	operationID := command.OperationID
	call := actionmodel.ActionCall{ActionName: command.ActionName, Input: append(json.RawMessage(nil), command.Input...)}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return operations.Task{}, ErrClosed
	}
	s, exists := r.states[operationID]
	if !exists {
		return operations.Task{}, operations.ErrUnknownOperation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := active(s); err != nil {
		return operations.Task{}, err
	}
	if err := validStep(s.operation, command.StepID); err != nil {
		return operations.Task{}, err
	}
	for _, question := range s.operation.Questions {
		if question.TaskID == "" && question.Pending() && (question.StepID == "" || question.StepID == command.StepID) {
			return operations.Task{}, ErrQuestionPending
		}
	}
	if len(s.operation.Tasks) >= 10000 {
		return operations.Task{}, errors.New("operation task limit exceeded")
	}
	task := queuedTask(operationID, command.StepID, "", actionmodel.NextAction{AgentID: command.AgentID, ActionName: call.ActionName, Input: call.Input})
	next := s.operation.Clone()
	next.Tasks = append(next.Tasks, task)
	refreshStatus(&next)
	appendEvent(&next, operations.TaskAssigned, task.ID, "", "Task queued")
	if err := r.save(ctx, s, next); err != nil {
		return operations.Task{}, err
	}
	r.signalScheduler()
	return next.Clone().Tasks[len(next.Tasks)-1], nil
}

func (r *Runner) runTask(s *operationState, task operations.Task, call actionmodel.ActionCall) {
	defer r.wg.Done()
	defer r.signalScheduler()
	ctx, cancel := context.WithDeadline(r.ctx, *task.DeadlineAt)
	defer cancel()
	var result actionmodel.ActionOutput
	var runErr error
	if dispatcher, ok := r.executor.(DurableActionExecutor); ok {
		result, runErr = dispatcher.DispatchAction(ctx, task.AgentID, call)
	} else {
		result, runErr = r.executor.RunAction(ctx, task.AgentID, call)
	}
	r.finishTask(s, task, result, runErr)
}

func (r *Runner) finishTask(s *operationState, task operations.Task, result actionmodel.ActionOutput, runErr error) {
	// Stopping the host suspends observation, not the remote action. Leave the
	// persisted running record for the next runner to reconcile.
	if runErr != nil && r.ctx.Err() != nil {
		return
	}
	interrupted := errors.Is(runErr, executions.ErrIndeterminate) || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled)
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.operation.Clone()
	for i := range next.Tasks {
		if next.Tasks[i].ID != task.ID {
			continue
		}
		finished := time.Now().UTC()
		next.Tasks[i].FinishedAt = &finished
		if runErr == nil {
			next.Tasks[i].Status, next.Tasks[i].Result = operations.TaskSucceeded, &result
			next.Tasks[i].Error = ""
		} else {
			next.Tasks[i].Status, next.Tasks[i].Error = operations.TaskFailed, runErr.Error()
			if interrupted {
				next.Tasks[i].Status = operations.TaskInterrupted
			}
		}
	}
	r.continueTask(&next, task.ID, runErr == nil)
	r.refreshGroups(&next)
	refreshStatus(&next)
	kind, message := operations.TaskFinished, "Task finished; inspect its result or error"
	if interrupted {
		kind, message = operations.TaskInterruptedEvent, "Execution outcome is unknown; inspect before explicitly assigning replacement work"
	}
	appendEvent(&next, kind, task.ID, "", message)
	// Preserve evidence and errors during shutdown, after the task context ends.
	saveCtx, done := context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
	defer done()
	if err := r.save(saveCtx, s, next); err != nil {
		s.err = errors.Join(s.err, fmt.Errorf("save task result: %w", err))
		close(s.changed)
		s.changed = make(chan struct{})
	}
}
