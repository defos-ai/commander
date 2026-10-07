package events

import (
	"context"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/id"
)

// Client sends receipts through a separate inbox, allowing agents to process
// confirmations while an action command is running.
type Client struct {
	Bus comms.Bus
}

func (c Client) Publish(ctx context.Context, message messages.Message) error {
	if c.Bus == nil {
		return errors.New("event bus is required")
	}
	if message.Type != ActionResultAccepted {
		return fmt.Errorf("unsupported agent event type %q", message.Type)
	}
	if err := message.Validate(); err != nil {
		return err
	}
	if !id.Valid(message.CorrelationID) {
		return errors.New("result receipt requires a command UUID")
	}
	var receipt ResultReceipt
	if err := domain.Decode(message.Payload, &receipt); err != nil {
		return err
	}
	return c.Bus.Publish(ctx, messages.AgentEvents(message.AgentID), message)
}
