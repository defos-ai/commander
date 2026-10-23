package runner

import (
	"context"
	"fmt"

	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
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

func (r *Runner) normalize(ctx context.Context, op operations.Operation, next []actionmodel.NextAction, records []agents.Agent) ([]actionmodel.NextAction, error) {
	if len(next) == 0 {
		return next, nil
	}
	if r.sessions != nil {
		return r.sessions.Normalize(ctx, next, records, op.Mission.ActionDefinitions)
	}
	return runtimeSessions.Normalize(next, records, op.Mission.ActionDefinitions...), nil
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
		records, err := r.catalog.Agents(ctx)
		if err != nil {
			return operations.Operation{}, err
		}

		command.Actions, err = r.normalize(ctx, s.operation, command.Actions, records)
		if err != nil {
			return operations.Operation{}, err
		}
		scopes := map[string]bool{}
		for _, a := range command.Actions {
			task := operations.NewTask(command.OperationID, "", "", a)
			if task.SessionIntent == nil {
				continue
			}
			if task.SessionIntent.Mode == "create" {
				if scopes[task.SessionIntent.ScopeID] {
					return operations.Operation{}, fmt.Errorf("%w: multiple creations in one scope", domain.ErrInvalidInput)
				}
				scopes[task.SessionIntent.ScopeID] = true
			}
			if r.sessions == nil {
				return operations.Operation{}, fmt.Errorf("%w: session persistence required", domain.ErrInvalidInput)
			}
			if err := r.sessions.CheckAdmission(ctx, task); err != nil {
				return operations.Operation{}, fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
			}
		}

		next, err := r.store.ResolveContinuation(ctx, command.OperationID, command.TaskID, command.Version, command.Actions)
		if err := s.acceptUpdate(next, err); err != nil {
			return operations.Operation{}, err
		}
		if r.sessions != nil {
			if err := r.sessions.Observe(ctx, runtimeSessions.Evidence{OperationID: command.OperationID}); err != nil {
				return operations.Operation{}, err
			}
		}
		r.signalScheduler()
		return s.operation.Clone(), nil
	})
}
