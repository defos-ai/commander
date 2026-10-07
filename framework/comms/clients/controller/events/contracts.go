package events

import (
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
)

const (
	AgentStarted    = messages.AgentStarted
	AgentHeartbeat  = messages.AgentHeartbeat
	AgentUpdated    = messages.AgentUpdated
	AgentStopped    = messages.AgentStopped
	ActionSucceeded = messages.ActionSucceeded
	CommandFailed   = messages.CommandFailed
)

type (
	AgentSnapshot = agentmodel.AgentSnapshot
	ActionOutput  = actionmodel.ActionOutput
	CommandError  = executions.CommandError
)
