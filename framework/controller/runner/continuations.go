package runner

import (
	"context"

	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
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
	return submit(ctx, r, func(ctx context.Context) (operations.Operation, error) {
		s, err := r.activeState(ctx, command.OperationID)
		if err != nil {
			return operations.Operation{}, err
		}
		next, err := r.store.ResolveContinuation(ctx, command.OperationID, command.TaskID, command.Version, command.Actions)
		if err := s.acceptUpdate(next, err); err != nil {
			return operations.Operation{}, err
		}
		r.signalScheduler()
		return s.operation.Clone(), nil
	})
}
