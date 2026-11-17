package fixtures

import (
	"context"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/agents"
	agentfakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
)

func NewStoppingController(t *testing.T) OwnedOperations {
	t.Helper()
	fixture := NewOwnedOperations(t)
	fixture.ResultGate.Open()
	fixture.Config.ActionStore = agentfakes.DiscardActionCatalog{}
	fixture.Config.LeaseSweepInterval = 20 * time.Millisecond
	return fixture
}

func StopSelectionRecord(agentID, kind, version string) agents.Agent {
	now := time.Now().UTC()
	return agents.Agent{AgentSnapshot: agents.AgentSnapshot{
		ID: agentID, Type: agents.AgentType(kind), Version: version, Status: agents.AgentReady,
		Server: agents.ServerInfo{IPAddress: "127.0.0.1", Hostname: "stop-selection"},
	}, ObservedAt: now, LastSeen: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Hour)}
}

func ObserveSnapshot(t *testing.T, ctx context.Context, bus *fakes.Bus, kind messages.MessageType, snapshot agents.AgentSnapshot, correlationID string) {
	t.Helper()
	message, err := messages.NewMessage(kind, snapshot.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	message.CorrelationID = correlationID
	if err := bus.Send(ctx, message); err != nil {
		t.Fatal(err)
	}
	if ack := Receive(t, ctx, bus.Acknowledged()); ack != message.ID {
		t.Fatalf("unexpected observation acknowledgement: %s, want %s", ack, message.ID)
	}
}
