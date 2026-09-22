// Package consumers processes inbound controller messages.
package consumers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"commander/framework/comms"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
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
	Subscription comms.Subscription
	Logger       *slog.Logger
	Bus          comms.Bus
	Inventory    agentmodel.AgentStore
	Actions      actionmodel.ActionStore
	Executions   executions.ExecutionStore
	State        *state.State
}

// Consume reads controller events and routes each one to its domain consumer.
// The transport consumer acknowledges successfully processed messages.
func (c EventConsumer) Consume(ctx context.Context) error {
	consumer := comms.Consumer{
		Subscription: c.Subscription,
		Logger:       c.Logger,
		ContinueOnError: func(_ messages.Message, err error) bool {
			return errors.Is(err, ErrInvalidMessage)
		},
	}
	agents := AgentEventConsumer{
		Inventory: c.Inventory, Actions: c.Actions, State: c.State,
	}
	results := CommandResultConsumer{
		Inventory: c.Inventory, Executions: c.Executions,
		Bus: c.Bus, State: c.State,
	}
	return consumer.Consume(ctx, func(ctx context.Context, message messages.Message) error {
		c.State.ObservationMu.Lock()
		defer c.State.ObservationMu.Unlock()

		if err := message.Validate(); err != nil {
			return InvalidMessage(err)
		}
		c.State.Mu.RLock()
		pending, exists := c.State.Pending[message.CorrelationID]
		wrongWork := exists && pending.Work != message.Work
		c.State.Mu.RUnlock()
		if wrongWork {
			return InvalidMessage(errors.New("reply work does not match the pending command"))
		}
		if err := c.State.CheckOpen(); err != nil {
			return err
		}

		switch message.Type {
		case controllerevents.AgentStarted, controllerevents.AgentHeartbeat, controllerevents.AgentUpdated, controllerevents.AgentStopped:
			return agents.Consume(ctx, message)
		case controllerevents.ActionSucceeded, controllerevents.CommandFailed:
			return results.Consume(ctx, message)
		default:
			return InvalidMessage(fmt.Errorf("unsupported event type %q", message.Type))
		}
	})
}
