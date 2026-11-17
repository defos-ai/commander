package fakes

import (
	"context"
	"sort"
	"sync/atomic"
	"time"

	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
)

type AgentCommands struct {
	commands.Controller
	inventory     agents.AgentStore
	calls         atomic.Int32
	failure       atomic.Pointer[agentStopFailure]
	targetFailure atomic.Pointer[agentStopTargetFailure]
}

type agentStopFailure struct {
	err error
}

type agentStopTargetFailure struct {
	id        string
	code      string
	afterSave bool
}

func NewAgentCommands(inventory agents.AgentStore) *AgentCommands {
	return &AgentCommands{inventory: inventory}
}

func (c *AgentCommands) StopAgents(ctx context.Context, selector agents.Selector) (agents.StopResult, error) {
	c.calls.Add(1)
	if err := selector.Validate(); err != nil {
		return agents.StopResult{}, err
	}
	records, err := c.inventory.List(ctx)
	if err != nil {
		return agents.StopResult{}, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	result := agents.StopResult{Agents: []agents.Agent{}}
	failure := c.targetFailure.Swap(nil)
	for _, record := range records {
		if !selector.Matches(record) {
			continue
		}
		if failure != nil && failure.id == record.ID && !failure.afterSave {
			result.Errors = append(result.Errors, agents.StopError{ID: record.ID, Error: failure.code})
			continue
		}
		stopped, err := c.inventory.RequestStop(ctx, record.ID, id.New(), time.Now().UTC())
		if err != nil {
			return agents.StopResult{}, err
		}
		if failure != nil && failure.id == record.ID {
			result.Errors = append(result.Errors, agents.StopError{ID: record.ID, Error: failure.code})
		} else {
			result.Agents = append(result.Agents, stopped)
		}
	}
	if failure := c.failure.Swap(nil); failure != nil {
		return agents.StopResult{}, failure.err
	}
	return result, nil
}

func (c *AgentCommands) FailNextStopAfterSave(err error) {
	c.failure.Store(&agentStopFailure{err: err})
}

func (c *AgentCommands) Calls() int {
	return int(c.calls.Load())
}

func (c *AgentCommands) FailNextTarget(agentID, code string, afterSave bool) {
	c.targetFailure.Store(&agentStopTargetFailure{id: agentID, code: code, afterSave: afterSave})
}
