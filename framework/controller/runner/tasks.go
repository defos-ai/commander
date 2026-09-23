package runner

import (
	"context"
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
	if err := command.Validate(); err != nil {
		return operations.Task{}, err
	}
	task := queuedTask(command.OperationID, command.StepID, "", actionmodel.NextAction{AgentID: command.AgentID, ActionName: command.ActionName, Input: command.Input})
	op, err := r.change(ctx, command.OperationID, func(next *operations.Operation) error {
		if err := validStep(*next, command.StepID); err != nil {
			return err
		}
		for _, question := range next.Questions {
			if question.TaskID == "" && question.Pending() && (question.StepID == "" || question.StepID == command.StepID) {
				return ErrQuestionPending
			}
		}
		if len(next.Tasks) >= 10000 {
			return errors.New("operation task limit exceeded")
		}
		next.Tasks = append(next.Tasks, task)
		refreshStatus(next)
		appendEvent(next, operations.TaskAssigned, task.ID, "", "Task queued")
		return nil
	})
	if err != nil {
		return operations.Task{}, err
	}
	r.signalScheduler()
	return op.Tasks[len(op.Tasks)-1], nil
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
	interrupted := errors.Is(runErr, executions.ErrIndeterminate) || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled)
	// Preserve evidence and errors during shutdown, after the task context ends.
	saveCtx, done := context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
	defer done()
	if err := r.update(saveCtx, s, func(next *operations.Operation) error {
		i := taskIndex(next, task.ID)
		if i < 0 || !needsTaskRecovery(next.Tasks[i]) {
			return operations.ErrOperationConflict
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
		r.continueTask(next, task.ID, runErr == nil)
		r.refreshGroups(next)
		refreshStatus(next)
		kind, message := operations.TaskFinished, "Task finished; inspect its result or error"
		if interrupted {
			kind, message = operations.TaskInterruptedEvent, "Execution outcome is unknown; inspect before explicitly assigning replacement work"
		}
		appendEvent(next, kind, task.ID, "", message)
		return nil
	}); err != nil {
		s.err = errors.Join(s.err, fmt.Errorf("save task result: %w", err))
		close(s.changed)
		s.changed = make(chan struct{})
	}
}
