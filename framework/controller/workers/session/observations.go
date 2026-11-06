package session

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

func (w *Worker) Observe(ctx context.Context, e runtimeSessions.Evidence) error {
	if err := w.lock(ctx); err != nil {
		return err
	}
	defer w.unlock()
	if e.OperationID != "" {
		op, err := w.operations.Get(ctx, e.OperationID)
		if err != nil {
			return err
		}
		for _, task := range op.Tasks {
			if task.SessionIntent == nil || task.SessionIntent.Error != "" {
				continue
			}
			creator, err := w.admit(ctx, task)
			if err != nil {
				if errors.Is(err, model.ErrScopeReserved) {
					continue
				}
				return err
			}
			if creator {
				intent := task.SessionIntent
				if task.AgentID != "" && w.bound[task.ID] != task.AgentID {
					_, err := w.apply(ctx, model.Change{Kind: model.Bind, ScopeID: intent.ScopeID, ID: intent.ID, OwnerAgentID: task.AgentID,
						CommandID: task.CommandID, OperationID: task.OperationID, TaskID: task.ID})
					if err == nil {
						w.bound[task.ID] = task.AgentID
					}
					if err != nil && !errors.Is(err, model.ErrSessionClosed) && !errors.Is(err, model.ErrSessionAbandoned) && !errors.Is(err, model.ErrSessionClosing) {
						return err
					}
				} else if task.AgentID == "" && task.Status == operations.TaskCancelled {
					_, err := w.apply(ctx, model.Change{Kind: model.CancelBeforeEffect, ScopeID: intent.ScopeID, ID: intent.ID, Control: &model.Control{RequestID: task.ID}})
					if err != nil {
						return err
					}
				}
			}
			if task.Result != nil && task.Result.Session != nil {
				item := task.Result.Session
				if item.ID != task.SessionIntent.ID || item.ScopeID != task.SessionIntent.ScopeID || item.OwnerAgentID != task.AgentID {
					return model.ErrConflict
				}
				if err := w.observeResource(ctx, *item); err != nil {
					return err
				}
			}
		}
	}
	if e.AgentID != "" {
		agent, err := w.agents.Get(ctx, e.AgentID)
		if errors.Is(err, agents.ErrUnknownAgent) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, item := range agent.Sessions {
			// Unknown resources do not acquire framework scope ownership through inventory.
			err := w.observeResource(ctx, item)
			if err != nil && !errors.Is(err, model.ErrUnknownSession) && !errors.Is(err, model.ErrConflict) {
				return err
			}
		}
	}
	return nil
}

func (w *Worker) observeResource(ctx context.Context, item model.Session) error {
	if w.observed[item.Reference] >= item.Revision {
		return nil
	}
	_, err := w.apply(ctx, model.Change{Kind: model.Observe, ScopeID: item.ScopeID, ID: item.ID, Observation: &item})
	if err == nil {
		w.observed[item.Reference] = item.Revision
	}
	return err
}
