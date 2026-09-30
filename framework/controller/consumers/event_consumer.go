package consumers

import (
	"context"
	"errors"
	"fmt"

	"commander/framework/comms"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/executions"
	"commander/framework/internal/state"
)

// ErrInvalidMessage identifies rejected input that should not stop other agents
// from reporting. Storage and transport failures must remain ordinary errors.
var ErrInvalidMessage = comms.ErrInvalidMessage

func InvalidMessage(err error) error {
	return comms.InvalidMessage(err)
}

type EventConsumer struct {
	Bus        comms.Bus
	Inventory  agentmodel.AgentStore
	Actions    actionmodel.ActionStore
	Executions executions.ExecutionStore
	Artifacts  artifacts.ArtifactStore
	State      *state.State
}

// Consume is called by the controller loop, together with commands and lease expiry.
func (c EventConsumer) Consume(ctx context.Context, message messages.Message) error {
	if err := message.Validate(); err != nil {
		return InvalidMessage(err)
	}
	if pending, exists := c.State.Pending[message.CorrelationID]; exists && pending.Work != message.Work {
		return InvalidMessage(errors.New("reply work does not match the pending command"))
	}
	if err := c.State.CheckOpen(); err != nil {
		return err
	}
	switch message.Type {
	case controllerevents.AgentStarted, controllerevents.AgentHeartbeat, controllerevents.AgentUpdated, controllerevents.AgentStopped:
		return (AgentEventConsumer{Inventory: c.Inventory, Actions: c.Actions, State: c.State}).Consume(ctx, message)
	case controllerevents.ActionSucceeded, controllerevents.CommandFailed:
		return (CommandResultConsumer{Inventory: c.Inventory, Executions: c.Executions, Artifacts: c.Artifacts, Bus: c.Bus, State: c.State}).Consume(ctx, message)
	default:
		return InvalidMessage(fmt.Errorf("unsupported event type %q", message.Type))
	}
}
