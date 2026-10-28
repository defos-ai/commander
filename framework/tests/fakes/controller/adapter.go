package fakes

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/comms"
	adapterevents "github.com/defos-ai/commander/framework/comms/clients/adapter/events"
	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/agents"
)

type AdapterWork struct {
	Context context.Context
	AgentID string
	Finish  chan agents.Agent
}
type AdapterBackend struct {
	commands.Controller
	started       chan AdapterWork
	calls         atomic.Int64
	waitForFinish bool
}

func (b *AdapterBackend) DiscoverActions(ctx context.Context, agentID string, _ *agents.DiscoveryScope) (agents.Agent, error) {
	b.calls.Add(1)
	work := AdapterWork{Context: ctx, AgentID: agentID, Finish: make(chan agents.Agent, 1)}
	select {
	case b.started <- work:
	case <-ctx.Done():
		return agents.Agent{}, ctx.Err()
	}
	if b.waitForFinish {
		return <-work.Finish, nil
	}
	select {
	case result := <-work.Finish:
		return result, nil
	case <-ctx.Done():
		return agents.Agent{}, ctx.Err()
	}
}

type UnusedOperations struct{ commands.Operations }

var ErrReplyPublication = errors.New("reply publication failed")

type AdapterBus struct {
	adapterID    string
	controllerID string
	topics       map[string]chan messages.Message
	replies      chan messages.Message
	acked        chan string
	rejected     chan string
	failReply    atomic.Bool
}

func (b *AdapterBus) Publish(ctx context.Context, topic string, m messages.Message) error {
	if m.Type == adapterevents.ControllerResponse {
		select {
		case b.replies <- m:
		case <-ctx.Done():
			return ctx.Err()
		}
		if b.failReply.Swap(false) {
			return ErrReplyPublication
		}
	}
	select {
	case b.topics[topic] <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *AdapterBus) Subscribe(_ context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	ownerID := b.controllerID
	if inbox.Topic == messages.AdapterReplies(b.adapterID) {
		ownerID = b.adapterID
	}
	topic, exists := b.topics[inbox.Topic]
	if !exists || inbox.Name != ownerID || inbox.Mode != comms.TransientExclusive {
		return nil, fmt.Errorf("unexpected inbox configuration: %+v", inbox)
	}
	return adapterSubscription{messages: topic, acked: b.acked, rejected: b.rejected}, nil
}
func (*AdapterBus) Close() error { return nil }

type adapterSubscription struct {
	messages <-chan messages.Message
	acked    chan<- string
	rejected chan<- string
}

func (s adapterSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case m := <-s.messages:
		return adapterDelivery{delivery: delivery{message: m, onAck: func(ctx context.Context) error {
			select {
			case s.acked <- m.ID:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, rejected: s.rejected}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type adapterDelivery struct {
	delivery
	rejected chan<- string
}

func (d adapterDelivery) Reject(ctx context.Context) error {
	select {
	case d.rejected <- d.message.ID:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (adapterSubscription) Close() error { return nil }

func NewAdapterBackend() *AdapterBackend              { return &AdapterBackend{started: make(chan AdapterWork, 16)} }
func (b *AdapterBackend) Started() <-chan AdapterWork { return b.started }
func (b *AdapterBackend) Calls() int64                { return b.calls.Load() }
func (b *AdapterBackend) WaitForFinish()              { b.waitForFinish = true }
func NewAdapterBus(adapterID, controllerID string) *AdapterBus {
	return &AdapterBus{
		adapterID: adapterID, controllerID: controllerID,
		topics: map[string]chan messages.Message{
			messages.ControllerCommands("default"):      make(chan messages.Message, 32),
			messages.ControllerCancellations("default"): make(chan messages.Message, 32),
			messages.AdapterReplies(adapterID):          make(chan messages.Message, 32),
		},
		replies: make(chan messages.Message, 32),
		acked:   make(chan string, 64), rejected: make(chan string, 64),
	}
}
func (b *AdapterBus) Replies() <-chan messages.Message           { return b.replies }
func (b *AdapterBus) Acknowledged() <-chan string                { return b.acked }
func (b *AdapterBus) Rejected() <-chan string                    { return b.rejected }
func (b *AdapterBus) Topic(topic string) <-chan messages.Message { return b.topics[topic] }
func (b *AdapterBus) FailNextReply()                             { b.failReply.Store(true) }
