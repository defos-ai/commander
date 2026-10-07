// Package agent provides independently hosted, controller-directed agents.
package agent

import (
	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
)

type (
	AgentStatus   = agentmodel.AgentStatus
	AgentType     = agentmodel.AgentType
	ActionCall    = actionmodel.ActionCall
	ActionOutput  = actionmodel.ActionOutput
	CommandError  = executions.CommandError
	AgentSnapshot = agentmodel.AgentSnapshot
	Work          = agentmodel.Work
)

const (
	GenericAgentType = agentmodel.GenericAgentType
	AgentIdle        = agentmodel.AgentIdle
	AgentDiscovering = agentmodel.AgentDiscovering
	AgentReady       = agentmodel.AgentReady
	AgentWorking     = agentmodel.AgentWorking
	AgentUnavailable = agentmodel.AgentUnavailable
	AgentStopped     = agentmodel.AgentStopped
)

func Decode(data []byte, destination any) error {
	return domain.Decode(data, destination)
}
