package fakes

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
)

const AgentStopFailureDetail = "fixture private agent stop failure sentinel"

type UncertainAgentStop struct {
	controllercommands.Controller
	inventory agents.AgentStore
	calls     atomic.Int32
}

func NewUncertainAgentStop(inventory agents.AgentStore) *UncertainAgentStop {
	return &UncertainAgentStop{inventory: inventory}
}

func (c *UncertainAgentStop) StopAgents(ctx context.Context, selector agents.StopSelector) (agents.StopResult, error) {
	c.calls.Add(1)
	record, err := c.inventory.RequestStop(ctx, selector.ID, id.New(), time.Now().UTC())
	if err != nil {
		return agents.StopResult{}, err
	}
	return agents.StopResult{Agents: []agents.Agent{record}}, errors.Join(context.DeadlineExceeded, agents.ErrUnknownAgent, errors.New(AgentStopFailureDetail))
}

func (c *UncertainAgentStop) Calls() int32 { return c.calls.Load() }

type FixedAgentStops struct {
	controllercommands.Controller
	result agents.StopResult
}

func NewFixedAgentStops(result agents.StopResult) *FixedAgentStops {
	return &FixedAgentStops{result: cloneStopResult(result)}
}

func (c *FixedAgentStops) StopAgents(context.Context, agents.StopSelector) (agents.StopResult, error) {
	return cloneStopResult(c.result), nil
}

func cloneStopResult(original agents.StopResult) agents.StopResult {
	result := agents.StopResult{Agents: make([]agents.Agent, 0, len(original.Agents)), Errors: append([]agents.StopError(nil), original.Errors...)}
	for _, item := range original.Agents {
		result.Agents = append(result.Agents, item.Clone())
	}
	return result
}
