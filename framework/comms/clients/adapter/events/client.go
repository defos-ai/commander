package events

import (
	"context"
	"encoding/json"
	"errors"

	"commander/framework/comms"
	"commander/framework/comms/messages"
	"commander/framework/id"
)

// Client publishes prepared replies without changing their delivery identities.
// The caller owns the bus and decides whether and when to retry a reply.
type Client struct{ Bus comms.Bus }

func NewReply(adapterID, requestID string, value any, commandErr error) (messages.Message, error) {
	reply := ControllerReply{}
	if commandErr == nil {
		reply.Result, commandErr = json.Marshal(value)
	}
	if commandErr != nil {
		reply.Error = EncodeError(commandErr)
	}
	m, err := messages.NewAdapterMessage(ControllerResponse, adapterID, reply)
	if err != nil {
		return messages.Message{}, err
	}
	m.CorrelationID = requestID
	return m, m.Validate()
}

func (c Client) Publish(ctx context.Context, message messages.Message) error {
	if c.Bus == nil {
		return errors.New("adapter event client requires a bus")
	}
	if err := message.Validate(); err != nil {
		return err
	}
	if message.Type != ControllerResponse || !id.Valid(message.CorrelationID) {
		return errors.New("adapter event must be a correlated controller reply")
	}
	var reply ControllerReply
	if err := json.Unmarshal(message.Payload, &reply); err != nil {
		return err
	}
	if err := reply.Validate(); err != nil {
		return err
	}
	return c.Bus.Publish(ctx, messages.AdapterReplies(message.AdapterID), message)
}
