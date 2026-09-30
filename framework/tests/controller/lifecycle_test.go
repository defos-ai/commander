package controller_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	artifactstore "commander/framework/artifact/store"
	"commander/framework/comms"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	"commander/framework/controller"
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/executions"
)

func TestControllerWaitsForCommandsIndependently(t *testing.T) {
	ctx, c, bus, _ := fixture(t, time.Hour)
	firstCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	first := discover(firstCtx, c)
	firstCommand := receive(t, ctx, bus.commands)
	second := discover(ctx, c)
	secondCommand := receive(t, ctx, bus.commands)

	bus.observe(t, ctx, controllerevents.AgentUpdated, secondCommand.ID)
	if err := receive(t, ctx, second); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-first:
		t.Fatalf("unrelated reply completed the first command: %v", err)
	default:
	}
	cancel()
	if err := receive(t, ctx, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation did not release caller: %v", err)
	}
	bus.observe(t, ctx, controllerevents.AgentUpdated, firstCommand.ID)
	third := discover(ctx, c)
	bus.observe(t, ctx, controllerevents.AgentUpdated, receive(t, ctx, bus.commands).ID)
	if err := receive(t, ctx, third); err != nil {
		t.Fatalf("late reply disrupted subsequent command: %v", err)
	}
}

func TestControllerCloseDoesNotWaitForBlockedObservation(t *testing.T) {
	ctx, c, bus, store := fixture(t, time.Hour)
	waiting := discover(ctx, c)
	receive(t, ctx, bus.commands)
	gate := newGate(t)
	store.upsertGate.Store(gate)
	heartbeat := bus.send(t, ctx, controllerevents.AgentHeartbeat, "")
	receive(t, ctx, gate.entered)

	deadline, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if err := c.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close did not respect deadline: %v", err)
	}
	if err := receive(t, ctx, waiting); !errors.Is(err, controller.ErrControllerClosed) {
		t.Fatalf("shutdown left command waiting: %v", err)
	}
	gate.open()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close could not be retried: %v", err)
	}
	select {
	case ack := <-bus.acked:
		t.Fatalf("cancelled observation %s was acknowledged: %s", heartbeat.ID, ack)
	default:
	}

}

func TestControllerOrdersLeaseExpiryBeforeRenewal(t *testing.T) {
	ctx, c, bus, store := fixture(t, time.Hour)
	waiting := discover(ctx, c)
	receive(t, ctx, bus.commands)
	gate := newGate(t)
	store.expiryGate.Store(gate)
	expired := store.record.Load().Clone()
	expired.LastHeartbeat = time.Now().Add(-2 * time.Hour)
	expired.LeaseExpiresAt = expired.LastHeartbeat.Add(time.Hour)
	store.record.Store(&expired)
	receive(t, ctx, gate.entered)
	heartbeat := bus.send(t, ctx, controllerevents.AgentHeartbeat, "")

	select {
	case ack := <-bus.acked:
		t.Fatalf("heartbeat overtook unfinished expiry: %s", ack)
	default:
	}
	gate.open()
	if err := receive(t, ctx, waiting); !errors.Is(err, controller.ErrAgentNotRunning) {
		t.Fatalf("expired command was not released: %v", err)
	}
	if ack := receive(t, ctx, bus.acked); ack != heartbeat.ID {
		t.Fatalf("unexpected acknowledgement: %s", ack)
	}
	record, err := c.WaitForAgent(ctx, agentID)
	if err != nil || record.Stale || record.Status != agents.AgentReady {
		t.Fatalf("renewal did not restore readiness: %+v, %v", record, err)
	}
	resumed := discover(ctx, c)
	bus.observe(t, ctx, controllerevents.AgentUpdated, receive(t, ctx, bus.commands).ID)
	if err := receive(t, ctx, resumed); err != nil {
		t.Fatalf("renewed agent could not accept work: %v", err)
	}
}

const agentID = "fixture-agent-00000001"

func fixture(t *testing.T, timeout time.Duration) (context.Context, *controller.Controller, *testBus, *inventory) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	bus := &testBus{events: make(chan messages.Message, 16), commands: make(chan messages.Message, 16), acked: make(chan string, 16), closed: make(chan struct{})}
	store := &inventory{}
	c, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: store, ActionStore: unusedActions{}, ExecutionStore: unusedExecutions{}, ArtifactStore: unusedArtifacts{},
		HeartbeatTimeout: timeout, LeaseSweepInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := c.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	bus.observe(t, ctx, controllerevents.AgentStarted, "")
	return ctx, c, bus, store
}

func discover(ctx context.Context, c *controller.Controller) <-chan error {
	done := make(chan error, 1)
	go func() { _, err := c.DiscoverActions(ctx, agentID, nil); done <- err }()
	return done
}

func receive[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatalf("expected result before deadline: %v", ctx.Err())
		var zero T
		return zero
	}
}

type testBus struct {
	events   chan messages.Message
	commands chan messages.Message
	acked    chan string
	closed   chan struct{}
}

func (b *testBus) Publish(ctx context.Context, _ string, m messages.Message) error {
	select {
	case b.commands <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *testBus) Subscribe(context.Context, string) (comms.Subscription, error) { return b, nil }
func (b *testBus) Next(ctx context.Context) (messages.Message, error) {
	select {
	case m := <-b.events:
		return m, nil
	case <-b.closed:
		return messages.Message{}, comms.ErrClosed
	case <-ctx.Done():
		return messages.Message{}, ctx.Err()
	}
}
func (b *testBus) Ack(_ context.Context, id string) error { b.acked <- id; return nil }
func (b *testBus) Close() error                           { close(b.closed); return nil }
func (b *testBus) send(t *testing.T, ctx context.Context, kind messages.MessageType, correlation string) messages.Message {
	t.Helper()
	snapshot := agents.AgentSnapshot{ID: agentID, Type: "fixture", Status: agents.AgentReady,
		Server: agents.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"}}
	m, err := messages.NewMessage(kind, agentID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	m.CorrelationID = correlation
	select {
	case b.events <- m:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return m
}
func (b *testBus) observe(t *testing.T, ctx context.Context, kind messages.MessageType, correlation string) {
	t.Helper()
	m := b.send(t, ctx, kind, correlation)
	if ack := receive(t, ctx, b.acked); ack != m.ID {
		t.Fatalf("unexpected acknowledgement: %s, want %s", ack, m.ID)
	}
}

type gate struct {
	entered chan struct{}
	release chan struct{}
}

func newGate(t *testing.T) *gate {
	g := &gate{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(g.open)
	return g
}
func (g *gate) open() {
	select {
	case <-g.release:
	default:
		close(g.release)
	}
}
func (g *gate) wait() {
	if g != nil {
		close(g.entered)
		// Simulate a provider that is slow to return even after cancellation.
		<-g.release
	}
}

type inventory struct {
	record     atomic.Pointer[agents.Agent]
	upsertGate atomic.Pointer[gate]
	expiryGate atomic.Pointer[gate]
}

func (s *inventory) Upsert(_ context.Context, record agents.Agent) error {
	s.upsertGate.Swap(nil).wait()
	cloned := record.Clone()
	s.record.Store(&cloned)
	return nil
}
func (s *inventory) Get(context.Context, string) (agents.Agent, error) {
	if record := s.record.Load(); record != nil {
		return record.Clone(), nil
	}
	return agents.Agent{}, agents.ErrUnknownAgent
}
func (s *inventory) List(context.Context) ([]agents.Agent, error) {
	if record := s.record.Load(); record != nil {
		return []agents.Agent{record.Clone()}, nil
	}
	return nil, nil
}
func (s *inventory) DeleteExpired(_ context.Context, cutoff time.Time) ([]agents.Agent, error) {
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

type unusedActions struct{ actions.ActionStore }
type unusedExecutions struct{ executions.ExecutionStore }
type unusedArtifacts struct{ artifactstore.ArtifactStore }
