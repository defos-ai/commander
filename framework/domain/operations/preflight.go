package operations

import (
	"slices"

	"github.com/defos-ai/commander/framework/domain/actions"
)

type Preflight struct {
	Ready   bool              `json:"ready"`
	Version uint64            `json:"version"`
	Actions []ActionReadiness `json:"actions"`
}

type ActionReadiness struct {
	ActionName        string           `json:"action_name"`
	ReadyAgentIDs     []string         `json:"ready_agent_ids,omitempty"`
	UnavailableAgents []AgentReadiness `json:"unavailable_agents,omitempty"`
}

type AgentReadiness struct {
	AgentID             string                `json:"agent_id"`
	Reason              string                `json:"reason"`
	MissingRequirements []actions.Requirement `json:"missing_requirements,omitempty"`
}

func (p Preflight) Clone() Preflight {
	p.Actions = slices.Clone(p.Actions)
	for i := range p.Actions {
		a := &p.Actions[i]
		a.ReadyAgentIDs = slices.Clone(a.ReadyAgentIDs)
		a.UnavailableAgents = slices.Clone(a.UnavailableAgents)
		for j := range a.UnavailableAgents {
			a.UnavailableAgents[j].MissingRequirements = slices.Clone(a.UnavailableAgents[j].MissingRequirements)
		}
	}
	return p
}
