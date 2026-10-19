package commands

import (
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
)

const (
	RunAction       = messages.RunAction
	DiscoverActions = messages.DiscoverActions
	StopAgent       = messages.StopAgent
	ControlSession  = messages.ControlSession
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
