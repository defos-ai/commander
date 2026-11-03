package fakes

import (
	"context"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

type subscriptionCleanup struct {
	gate *Gate
	err  error
}

type routedSubscription struct {
	messages chan messages.Message
	closed   chan struct{}
	closing  atomic.Bool
	closes   atomic.Int32
	cleanup  subscriptionCleanup
}

func (s *routedSubscription) publish(ctx context.Context, message messages.Message) error {
	select {
	case <-s.closed:
		return comms.ErrClosed
	default:
	}
	select {
	case s.messages <- message:
		return nil
	case <-s.closed:
		return comms.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *routedSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case message := <-s.messages:
		settle := func(context.Context) error { return nil }
		return delivery{message: message, onAck: settle, onReject: settle}, nil
	case <-s.closed:
		return nil, comms.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *routedSubscription) Close() error {
	s.closes.Add(1)
	if s.closing.CompareAndSwap(false, true) {
		s.cleanup.gate.wait()
		close(s.closed)
	} else {
		<-s.closed
	}
	return s.cleanup.err
}

func (b *Bus) route(topic string) *routedSubscription {
	b.routingMu.Lock()
	defer b.routingMu.Unlock()
	return b.routes[topic]
}

func (b *Bus) BlockInboxClose(topic string, gate *Gate, err error) {
	b.routingMu.Lock()
	defer b.routingMu.Unlock()
	b.closeRules[topic] = subscriptionCleanup{gate: gate, err: err}
}

func (b *Bus) InboxClosed(topic string) <-chan struct{} { return b.route(topic).closed }
func (b *Bus) InboxCloseCalls(topic string) int         { return int(b.route(topic).closes.Load()) }
