package consumers

import (
	"context"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/comms"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/internal/state"
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
	Secrets    *SecretEventConsumer
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
	case controllerevents.CheckpointRequested:
		return (CheckpointConsumer{Executions: c.Executions, Bus: c.Bus}).Consume(ctx, message)
	case controllerevents.SecretSyncRequested, controllerevents.SecretInstalled:
		if c.Secrets == nil {
			return InvalidMessage(errors.New("secret distribution is not configured"))
		}
		return c.Secrets.Consume(ctx, message)
	default:
		return InvalidMessage(fmt.Errorf("unsupported event type %q", message.Type))
	}
}
