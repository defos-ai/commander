package fakes

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
)

type Bus struct {
	events   chan messages.Message
	commands chan messages.Message
	acked    chan string
	closed   chan struct{}
	inbox    comms.Inbox
}

func (b *Bus) Publish(ctx context.Context, _ string, m messages.Message) error {
	select {
	case b.commands <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *Bus) Subscribe(_ context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	b.inbox = inbox
	return b, nil
}
func (b *Bus) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case m := <-b.events:
		return delivery{message: m, onAck: func(context.Context) error {
			b.acked <- m.ID
			return nil
		}}, nil
	case <-b.closed:
		return nil, comms.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (b *Bus) Close() error { close(b.closed); return nil }

type delivery struct {
	message messages.Message
	onAck   func(context.Context) error
}

func (d delivery) Message() messages.Message     { return d.message }
func (d delivery) Ack(ctx context.Context) error { return d.onAck(ctx) }
func (delivery) Reject(context.Context) error    { return nil }

type Gate struct {
	entered chan struct{}
	release chan struct{}
}

func (g *Gate) Open() {
	select {
	case <-g.release:
	default:
		close(g.release)
	}
}
func (g *Gate) wait() {
	if g != nil {
		close(g.entered)
		// Simulate a provider that is slow to return even after cancellation.
		<-g.release
	}
}

type Inventory struct {
	record     atomic.Pointer[agents.Agent]
	upsertGate atomic.Pointer[Gate]
	expiryGate atomic.Pointer[Gate]
}

func (s *Inventory) Upsert(_ context.Context, record agents.Agent) error {
	s.upsertGate.Swap(nil).wait()
	cloned := record.Clone()
	s.record.Store(&cloned)
	return nil
}
func (s *Inventory) Get(context.Context, string) (agents.Agent, error) {
	if record := s.record.Load(); record != nil {
		return record.Clone(), nil
	}
	return agents.Agent{}, agents.ErrUnknownAgent
}
func (s *Inventory) List(context.Context) ([]agents.Agent, error) {
	if record := s.record.Load(); record != nil {
		return []agents.Agent{record.Clone()}, nil
	}
	return nil, nil
}
func (s *Inventory) DeleteExpired(_ context.Context, cutoff time.Time) ([]agents.Agent, error) {
	for {
		record := s.record.Load()
		if record == nil || record.LastHeartbeat.After(cutoff) {
			return nil, nil
		}
		s.expiryGate.Swap(nil).wait()
		if s.record.CompareAndSwap(record, nil) {
			return []agents.Agent{record.Clone()}, nil
		}
	}
}

type UnusedActions struct{ actions.ActionStore }
type UnusedExecutions struct{ executions.ExecutionStore }
type UnusedArtifacts struct{ artifacts.ArtifactStore }

func NewBus() *Bus {
	return &Bus{events: make(chan messages.Message, 16), commands: make(chan messages.Message, 16), acked: make(chan string, 16), closed: make(chan struct{})}
}
func (b *Bus) Commands() <-chan messages.Message { return b.commands }
func (b *Bus) Acknowledged() <-chan string       { return b.acked }
func (b *Bus) Closed() <-chan struct{}           { return b.closed }
func (b *Bus) Inbox() comms.Inbox                { return b.inbox }
func (b *Bus) Send(ctx context.Context, message messages.Message) error {
	select {
	case b.events <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func NewGate() *Gate                     { return &Gate{entered: make(chan struct{}), release: make(chan struct{})} }
func (g *Gate) Entered() <-chan struct{} { return g.entered }
func (s *Inventory) Snapshot() *agents.Agent {
	if record := s.record.Load(); record != nil {
		copy := record.Clone()
		return &copy
	}
	return nil
}
func (s *Inventory) Set(record *agents.Agent) {
	if record == nil {
		s.record.Store(nil)
		return
	}
	copy := record.Clone()
	s.record.Store(&copy)
}
func (s *Inventory) BlockNextUpsert(gate *Gate) { s.upsertGate.Store(gate) }
func (s *Inventory) BlockNextExpiry(gate *Gate) { s.expiryGate.Store(gate) }
