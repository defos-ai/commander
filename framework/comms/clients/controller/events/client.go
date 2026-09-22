package events

import (
	"context"
	"errors"
	"fmt"

	"commander/framework/comms"
	"commander/framework/comms/messages"
)

// Client owns routing to the controller event inbox. Callers retain
// result messages when retrying so their IDs and payloads remain unchanged.
type Client struct {
	Bus comms.Bus
}

func (c Client) Publish(ctx context.Context, message messages.Message) error {
	if c.Bus == nil {
		return errors.New("event bus is required")
	}
	switch message.Type {
	case AgentStarted, AgentHeartbeat, AgentUpdated, AgentStopped, ActionSucceeded, CommandFailed:
	default:
		return fmt.Errorf("unsupported controller event type %q", message.Type)
	}
	if err := message.Validate(); err != nil {
		return err
	}
	return c.Bus.Publish(ctx, messages.ControllerEvents(messages.DefaultControllerType), message)
}

func (c Client) State(ctx context.Context, kind messages.MessageType, description AgentSnapshot, correlationID string) error {
	switch kind {
	case AgentStarted, AgentHeartbeat, AgentUpdated, AgentStopped:
	default:
		return fmt.Errorf("unsupported agent state event type %q", kind)
	}
	message, err := messages.NewMessage(kind, description.ID, description)
	if err != nil {
		return err
	}
	message.CorrelationID = correlationID
	return c.Publish(ctx, message)
}
