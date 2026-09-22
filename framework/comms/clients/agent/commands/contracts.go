// Package commands defines commands addressed to agents and publishes them over
// the bus. Execution policy and result tracking belong to the caller.
package commands

import (
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
)

const (
	RunAction       = messages.RunAction
	DiscoverActions = messages.DiscoverActions
	StopAgent       = messages.StopAgent
)

type ActionCall = actionmodel.ActionCall

type DiscoverActionsCommand struct {
	Scope *agentmodel.DiscoveryScope `json:"scope,omitempty"`
}

type StopAgentCommand struct{}

// NewRunAction preserves an operation task's identity as its command identity so
// durable execution records can recognize the same work after a restart.
func NewRunAction(agentID string, call ActionCall) (messages.Message, error) {
	if err := call.Validate(); err != nil {
		return messages.Message{}, err
	}
	command, err := messages.NewMessage(RunAction, agentID, call)
	if err != nil {
		return messages.Message{}, err
	}
	command.Work = call.Work
	if call.Work.TaskID != "" {
		command.ID = call.Work.TaskID
	}
	return command, nil
}

func NewDiscoverActions(agentID string, scope *agentmodel.DiscoveryScope) (messages.Message, error) {
	return messages.NewMessage(DiscoverActions, agentID, DiscoverActionsCommand{Scope: scope.Clone()})
}

func NewStopAgent(agentID string) (messages.Message, error) {
	return messages.NewMessage(StopAgent, agentID, StopAgentCommand{})
}
