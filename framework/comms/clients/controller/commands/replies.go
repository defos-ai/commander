package commands

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/defos-ai/commander/framework/comms"
	adapterevents "github.com/defos-ai/commander/framework/comms/clients/adapter/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/id"
)

type pendingCall struct {
	id    string
	reply chan adapterevents.ControllerReply
}

type correlatedReply struct {
	id    string
	reply adapterevents.ControllerReply
}

// Registration and delivery share one owner. Publication and response waits
// stay in callers, so a slow request cannot block unrelated correlations.
func (c *Client) run() {
	defer close(c.done)
	pending := make(map[string]chan adapterevents.ControllerReply)
	for c.ctx.Err() == nil {
		select {
		case <-c.ctx.Done():
		case call := <-c.register:
			pending[call.id] = call.reply
		case id := <-c.release:
			delete(pending, id)
		case response := <-c.replies:
			if waiting := pending[response.id]; waiting != nil {
				delete(pending, response.id)
				waiting <- response.reply
			}
		}
	}
}

func (c *Client) consumeReplies(logger *slog.Logger) {
	defer close(c.consumerDone)
	consumer := comms.Consumer{Subscription: c.subscription, Logger: logger,
		ContinueOnError: func(_ messages.Message, err error) bool {
			return errors.Is(err, comms.ErrInvalidMessage)
		},
	}
	err := consumer.Consume(c.ctx, func(ctx context.Context, m messages.Message) error {
		if err := m.Validate(); err != nil {
			return comms.InvalidMessage(err)
		}
		if m.Type != adapterevents.ControllerResponse || m.AdapterID != c.adapterID {
			return comms.InvalidMessage(errors.New("unexpected controller reply"))
		}
		var reply adapterevents.ControllerReply
		if err := json.Unmarshal(m.Payload, &reply); err != nil {
			return comms.InvalidMessage(err)
		}
		if !id.Valid(m.CorrelationID) {
			return comms.InvalidMessage(errors.New("invalid controller reply correlation"))
		}
		if err := reply.Validate(); err != nil {
			return comms.InvalidMessage(err)
		}
		select {
		case c.replies <- correlatedReply{id: m.CorrelationID, reply: reply}:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	})
	c.cancel(err)
}
