package runner

import (
	"context"
	"time"

	commands "commander/framework/comms/clients/controller/commands"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/operations"
)

func queuedTask(operationID, stepID, parentID string, action actionmodel.NextAction) operations.Task {
	return operations.NewTask(operationID, stepID, parentID, action)
}

func taskIndex(op *operations.Operation, taskID string) int {
	for i := range op.Tasks {
		if op.Tasks[i].ID == taskID {
			return i
		}
	}
	return -1
}

func (r *Runner) Resolve(ctx context.Context, command commands.ResolveContinuationCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	command.Actions = actionmodel.CloneNextActions(command.Actions)
	result, err := r.change(ctx, command.OperationID, func(op *operations.Operation) error {
		return op.ResolveContinuation(command.TaskID, command.Version, command.Actions)
	})
	if err == nil {
		r.signalScheduler()
	}
	return result, err
}

func cancelQueued(op *operations.Operation) {
	now := time.Now().UTC()
	for i := range op.Tasks {
		if op.Tasks[i].Status == operations.TaskQueued {
			op.Tasks[i].Status = operations.TaskCancelled
			op.Tasks[i].FinishedAt = &now
		}
	}
}
