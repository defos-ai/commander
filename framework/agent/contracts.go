// Package agent provides independently hosted, controller-directed agents.
package agent

import (
	"commander/framework/domain"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/executions"
)

// Shared contracts live in domain/agents, domain/actions, and domain/executions.
// These aliases keep agent extensions
// convenient without making consumers depend on the runtime package.
type (
	AgentStatus   = agentmodel.AgentStatus
	AgentType     = agentmodel.AgentType
	ActionCall    = actionmodel.ActionCall
	ActionOutput  = actionmodel.ActionOutput
	CommandError  = executions.CommandError
	AgentSnapshot = agentmodel.AgentSnapshot
)

const (
	GenericAgentType         = agentmodel.GenericAgentType
	AgentIdle                = agentmodel.AgentIdle
	AgentDiscovering         = agentmodel.AgentDiscovering
	AgentReady               = agentmodel.AgentReady
	AgentWorking             = agentmodel.AgentWorking
	AgentUnavailable         = agentmodel.AgentUnavailable
	AgentStopped             = agentmodel.AgentStopped
	DefaultInlineOutputLimit = actionmodel.DefaultInlineOutputLimit
)

// Decode validates and decodes an agent payload.
func Decode(data []byte, destination any) error {
	return domain.Decode(data, destination)
}
