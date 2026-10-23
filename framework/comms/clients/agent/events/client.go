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

// Client sends receipts, secret deliveries and checkpoint responses through a
// separate inbox while an action command is running.
type Client struct {
	Bus comms.Bus
}

func (c Client) Publish(ctx context.Context, message messages.Message) error {
	if ctx == nil || c.Bus == nil {
		return errors.New("agent event client requires a context and bus")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return err
	}
	switch message.Type {
	case ActionResultAccepted:
		if !id.Valid(message.CorrelationID) {
			return errors.New("result receipt requires a command UUID")
		}
		var receipt ResultReceipt
		if err := domain.Decode(message.Payload, &receipt); err != nil {
			return err
		}
	case SecretDelivered:
		if _, err := DecodeSecretDelivery(message); err != nil {
			return err
		}
	case CheckpointResponded:
		if _, err := DecodeCheckpointResponse(message); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported agent event type %q", message.Type)
	}
	return c.Bus.Publish(ctx, messages.AgentEvents(message.AgentID), message)
}
