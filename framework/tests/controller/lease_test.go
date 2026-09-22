package controller_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agents "commander/framework/agent"
	"commander/framework/controller"
	agentmodel "commander/framework/domain/agents"
)

func TestControllerRestartPreservesIssuedLeaseDeadline(t *testing.T) {
	// Arrange: a five-second lease expired, but its heartbeat is still within
	// the replacement controller's default 45-second timeout.
	f := newDeliveryFixture(t)
	heartbeat := time.Now().UTC().Add(-10 * time.Second)
	expires := heartbeat.Add(5 * time.Second)
	if err := f.database.AgentStore.Upsert(t.Context(), agentmodel.Agent{
		AgentSnapshot: agents.AgentSnapshot{
			ID: recoveryAgentID, Type: "fixture", Status: agents.AgentReady,
		},
		LastSeen: heartbeat, LastHeartbeat: heartbeat, LeaseExpiresAt: expires,
	}); err != nil {
		t.Fatal(err)
	}

	// Act: restart against the saved inventory, query the agent and try a command.
	f.restart(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	record, readErr := f.controller.Agent(ctx, recoveryAgentID)
	_, commandErr := f.controller.DiscoverActions(ctx, recoveryAgentID, nil)

	// Assert: restart must not silently renew a lease or dispatch work to it.
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !record.Stale || record.Status != agents.AgentUnavailable {
		t.Fatalf("expired agent was reported available: %+v", record)
	}
	if !record.LeaseExpiresAt.Equal(expires) {
		t.Fatalf("restart changed issued lease deadline: got %s, want %s", record.LeaseExpiresAt, expires)
	}
	if !errors.Is(commandErr, controller.ErrAgentNotRunning) {
		t.Fatalf("expired lease must reject commands: %v", commandErr)
	}
	if f.bus.published.Load() != 0 {
		t.Fatal("command was dispatched to an agent with an expired lease")
	}
}
