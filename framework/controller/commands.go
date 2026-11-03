package controller

import (
	"context"
	"github.com/defos-ai/commander/framework/controller/workers/coordination"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
)

type CommandFailure = executions.CommandFailure

func (c *Controller) DiscoverActions(ctx context.Context, agentID string, scope *agents.DiscoveryScope) (agents.Agent, error) {
	return request(ctx, c, func(ctx context.Context) (agents.Agent, error) {
		return c.coordination.DiscoverActions(ctx, agentID, scope)
	})
}
func (c *Controller) RunAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return request(ctx, c, func(ctx context.Context) (actions.ActionOutput, error) {
		return c.coordination.RunAction(ctx, agentID, call)
	})
}
func (c *Controller) StopAgent(ctx context.Context, agentID string) (agents.Agent, error) {
	return request(ctx, c, func(ctx context.Context) (agents.Agent, error) {
		return c.coordination.StopAgent(ctx, agentID)
	})
}
func (c *Controller) DispatchAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	var publisher coordination.SessionPublisher
	if c.sessions != nil {
		publisher = c.sessions
	}
	return request(ctx, c, func(ctx context.Context) (actions.ActionOutput, error) {
		return c.coordination.DispatchAction(ctx, agentID, call, publisher)
	})
}
func (c *Controller) RecoverAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return request(ctx, c, func(ctx context.Context) (actions.ActionOutput, error) {
		return c.coordination.RecoverAction(ctx, agentID, call)
	})
}
