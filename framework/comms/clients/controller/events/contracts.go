package events

import (
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/executions"
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
