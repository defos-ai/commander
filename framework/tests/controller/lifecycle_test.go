package controller_test

import (
	"context"
	"errors"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/domain/agents"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestControllerWaitsForCommandsIndependently(t *testing.T) {
	ctx, c, bus, _ := fixtures.Controller(t, time.Hour)
	firstCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	first := fixtures.Discover(firstCtx, c)
	firstCommand := fixtures.Receive(t, ctx, bus.Commands())
	second := fixtures.Discover(ctx, c)
	secondCommand := fixtures.Receive(t, ctx, bus.Commands())

	fixtures.Observe(t, ctx, bus, controllerevents.AgentUpdated, secondCommand.ID)
	if err := fixtures.Receive(t, ctx, second); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-first:
		t.Fatalf("unrelated reply completed the first command: %v", err)
	default:
	}
	cancel()
	if err := fixtures.Receive(t, ctx, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation did not release caller: %v", err)
	}
	fixtures.Observe(t, ctx, bus, controllerevents.AgentUpdated, firstCommand.ID)
	third := fixtures.Discover(ctx, c)
	fixtures.Observe(t, ctx, bus, controllerevents.AgentUpdated, fixtures.Receive(t, ctx, bus.Commands()).ID)
	if err := fixtures.Receive(t, ctx, third); err != nil {
		t.Fatalf("late reply disrupted subsequent command: %v", err)
	}
}

func TestControllerCloseDoesNotWaitForBlockedObservation(t *testing.T) {
	ctx, c, bus, store := fixtures.Controller(t, time.Hour)
	waiting := fixtures.Discover(ctx, c)
	fixtures.Receive(t, ctx, bus.Commands())
	gate := fixtures.NewGate(t)
	store.BlockNextUpsert(gate)
	heartbeat := fixtures.Send(t, ctx, bus, controllerevents.AgentHeartbeat, "")
	fixtures.Receive(t, ctx, gate.Entered())

	deadline, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if err := c.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close did not respect deadline: %v", err)
	}
	if err := fixtures.Receive(t, ctx, waiting); !errors.Is(err, controller.ErrControllerClosed) {
		t.Fatalf("shutdown left command waiting: %v", err)
	}
	gate.Open()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close could not be retried: %v", err)
	}
	select {
	case ack := <-bus.Acknowledged():
		t.Fatalf("cancelled observation %s was acknowledged: %s", heartbeat.ID, ack)
	default:
	}

}

func TestControllerOrdersLeaseExpiryBeforeRenewal(t *testing.T) {
	ctx, c, bus, store := fixtures.Controller(t, time.Hour)
	waiting := fixtures.Discover(ctx, c)
	fixtures.Receive(t, ctx, bus.Commands())
	gate := fixtures.NewGate(t)
	store.BlockNextExpiry(gate)
	expired := store.Snapshot().Clone()
	expired.LastHeartbeat = time.Now().Add(-2 * time.Hour)
	expired.LeaseExpiresAt = expired.LastHeartbeat.Add(time.Hour)
	store.Set(&expired)
	fixtures.Receive(t, ctx, gate.Entered())
	heartbeat := fixtures.Send(t, ctx, bus, controllerevents.AgentHeartbeat, "")

	select {
	case ack := <-bus.Acknowledged():
		t.Fatalf("heartbeat overtook unfinished expiry: %s", ack)
	default:
	}
	gate.Open()
	if err := fixtures.Receive(t, ctx, waiting); !errors.Is(err, controller.ErrAgentNotRunning) {
		t.Fatalf("expired command was not released: %v", err)
	}
	if ack := fixtures.Receive(t, ctx, bus.Acknowledged()); ack != heartbeat.ID {
		t.Fatalf("unexpected acknowledgement: %s", ack)
	}
	record, err := c.WaitForAgent(ctx, fixtures.AgentID)
	if err != nil || record.Stale || record.Status != agents.AgentReady {
		t.Fatalf("renewal did not restore readiness: %+v, %v", record, err)
	}
	resumed := fixtures.Discover(ctx, c)
	fixtures.Observe(t, ctx, bus, controllerevents.AgentUpdated, fixtures.Receive(t, ctx, bus.Commands()).ID)
	if err := fixtures.Receive(t, ctx, resumed); err != nil {
		t.Fatalf("renewed agent could not accept work: %v", err)
	}
}
