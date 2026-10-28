package fixtures

import (
	"context"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/domain/agents"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
)

const AgentID = "fixture-agent-00000001"

func Controller(t *testing.T, timeout time.Duration) (context.Context, *controller.Controller, *fakes.Bus, *fakes.Inventory) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	bus := fakes.NewBus()
	store := &fakes.Inventory{}
	c, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: store, ActionStore: fakes.UnusedActions{}, ExecutionStore: fakes.UnusedExecutions{}, ArtifactStore: fakes.UnusedArtifacts{},
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
	if want := (comms.Inbox{Topic: messages.ControllerEvents(c.Type()), Name: c.ID(), Mode: comms.TransientExclusive}); bus.Inbox() != want {
		t.Fatalf("controller event inbox=%+v, want %+v", bus.Inbox(), want)
	}
	Observe(t, ctx, bus, controllerevents.AgentStarted, "")
	return ctx, c, bus, store
}

func Discover(ctx context.Context, c *controller.Controller) <-chan error {
	done := make(chan error, 1)
	go func() { _, err := c.DiscoverActions(ctx, AgentID, nil); done <- err }()
	return done
}

func Receive[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
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

func Send(t *testing.T, ctx context.Context, b *fakes.Bus, kind messages.MessageType, correlation string) messages.Message {
	t.Helper()
	snapshot := agents.AgentSnapshot{ID: AgentID, Type: "fixture", Status: agents.AgentReady,
		Server: agents.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"}}
	m, err := messages.NewMessage(kind, AgentID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	m.CorrelationID = correlation
	if err := b.Send(ctx, m); err != nil {
		t.Fatal(err)
	}
	return m
}
func Observe(t *testing.T, ctx context.Context, b *fakes.Bus, kind messages.MessageType, correlation string) {
	t.Helper()
	m := Send(t, ctx, b, kind, correlation)
	if ack := Receive(t, ctx, b.Acknowledged()); ack != m.ID {
		t.Fatalf("unexpected acknowledgement: %s, want %s", ack, m.ID)
	}
}

func NewGate(t *testing.T) *fakes.Gate {
	g := fakes.NewGate()
	t.Cleanup(g.Open)
	return g
}
