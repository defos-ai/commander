package sessions

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

// Evidence names authoritative saved work or inventory. Callers cannot patch records.
type Evidence struct{ OperationID, AgentID string }

func (c *Coordinator) Observe(ctx context.Context, e Evidence) error {
	if err := c.lock(ctx); err != nil {
		return err
	}
	defer c.unlock()
	if e.OperationID != "" {
		op, err := c.operations.Get(ctx, e.OperationID)
		if err != nil {
			return err
		}
		for _, task := range op.Tasks {
			if task.SessionIntent == nil || task.SessionIntent.Error != "" {
				continue
			}
			if task.SessionIntent.Mode == model.Create {
				if err := c.admit(ctx, task); err != nil {
					if errors.Is(err, model.ErrScopeReserved) {
						continue
					}
					return err
				}
				intent := task.SessionIntent
				if task.AgentID != "" && c.bound[task.ID] != task.AgentID {
					_, err := c.apply(ctx, model.Change{Kind: model.Bind, ScopeID: intent.ScopeID, ID: intent.ID, OwnerAgentID: task.AgentID, Creation: creation(task)})
					if err == nil {
						c.bound[task.ID] = task.AgentID
					}
					if err != nil && !errors.Is(err, model.ErrSessionClosed) && !errors.Is(err, model.ErrSessionAbandoned) && !errors.Is(err, model.ErrSessionClosing) {
						return err
					}
				} else if task.AgentID == "" && task.Status == operations.TaskCancelled {
					_, err := c.apply(ctx, model.Change{Kind: model.CancelBeforeEffect, ScopeID: intent.ScopeID, ID: intent.ID, Control: &model.Control{RequestID: task.ID}})
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
				if err := c.observeResource(ctx, *item); err != nil {
					return err
				}
			}
		}
	}
	if e.AgentID != "" {
		agent, err := c.agents.Get(ctx, e.AgentID)
		if errors.Is(err, agents.ErrUnknownAgent) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, item := range agent.Sessions {
			// Unknown resources do not acquire framework scope ownership through inventory.
			err := c.observeResource(ctx, item)
			if err != nil && !errors.Is(err, model.ErrUnknownSession) && !errors.Is(err, model.ErrConflict) {
				return err
			}
		}
	}
	return nil
}

func (c *Coordinator) observeResource(ctx context.Context, item model.Session) error {
	if c.observed[item.Reference] >= item.Revision {
		return nil
	}
	_, err := c.apply(ctx, model.Change{Kind: model.Observe, ScopeID: item.ScopeID, ID: item.ID, Observation: &item})
	if err == nil {
		c.observed[item.Reference] = item.Revision
	}
	return err
}
