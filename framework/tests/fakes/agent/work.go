package fakes

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

type WorkBus struct {
	ResultBus
	agentID  string
	commands chan messages.Message
}

func (b *WorkBus) Subscribe(_ context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	if inbox.Name != b.agentID || inbox.Mode != comms.TransientExclusive {
		return nil, errors.New("agent inbox must be transient and owned by its process ID")
	}
	switch inbox.Topic {
	case "agents." + b.agentID + ".commands":
		return workSubscription{commands: b.commands}, nil
	case "agents." + b.agentID + ".events":
		return idleSubscription{}, nil
	default:
		return nil, errors.New("unexpected agent destination: " + inbox.Topic)
	}
}

type workSubscription struct {
	idleSubscription
	commands <-chan messages.Message
}

func (s workSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case message := <-s.commands:
		return workDelivery{message: message}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type workDelivery struct {
	message messages.Message
}

func (d workDelivery) Message() messages.Message  { return d.message }
func (workDelivery) Ack(context.Context) error    { return nil }
func (workDelivery) Reject(context.Context) error { return nil }

func NewWorkBus(agentID string) *WorkBus {
	return &WorkBus{agentID: agentID, ResultBus: *NewResultBus(), commands: make(chan messages.Message, 1)}
}
func (b *WorkBus) SendCommand(ctx context.Context, message messages.Message) error {
	select {
	case b.commands <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type idleSubscription struct{}

func (idleSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (idleSubscription) Close() error { return nil }
