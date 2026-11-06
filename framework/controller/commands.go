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
		return c.actionExecutor().RunAction(ctx, agentID, call)
	})
}
func (c *Controller) StopAgent(ctx context.Context, agentID string) (agents.Agent, error) {
	return request(ctx, c, func(ctx context.Context) (agents.Agent, error) {
		return c.coordination.StopAgent(ctx, agentID)
	})
}
func (c *Controller) DispatchAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return request(ctx, c, func(ctx context.Context) (actions.ActionOutput, error) {
		return c.actionExecutor().DispatchAction(ctx, agentID, call)
	})
}

func (c *Controller) actionExecutor() coordination.Executor {
	executor := coordination.Executor{Worker: c.coordination}
	if c.sessions != nil {
		executor.Sessions = c.sessions
	}
	return executor
}
func (c *Controller) RecoverAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return request(ctx, c, func(ctx context.Context) (actions.ActionOutput, error) {
		return c.coordination.RecoverAction(ctx, agentID, call)
	})
}
