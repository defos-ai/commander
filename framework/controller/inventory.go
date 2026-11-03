package controller

import (
	"context"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func (c *Controller) Agents(ctx context.Context) ([]agents.Agent, error) {
	return request(ctx, c, c.coordination.Agents)
}
func (c *Controller) Agent(ctx context.Context, agentID string) (agents.Agent, error) {
	return request(ctx, c, func(ctx context.Context) (agents.Agent, error) {
		return c.coordination.Agent(ctx, agentID)
	})
}
func (c *Controller) WaitForAgent(ctx context.Context, agentID string) (agents.Agent, error) {
	return request(ctx, c, func(ctx context.Context) (agents.Agent, error) {
		return c.coordination.WaitForAgent(ctx, agentID)
	})
}
