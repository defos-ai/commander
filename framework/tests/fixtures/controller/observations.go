package fixtures

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/internal/state"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
)

func ObservationConsumer(t *testing.T) (consumers.AgentEventConsumer, *fakes.Inventory, *fakes.ObservationCatalog) {
	t.Helper()
	store, catalog := &fakes.Inventory{}, &fakes.ObservationCatalog{}
	return consumers.AgentEventConsumer{Inventory: store, Actions: catalog, State: state.New(t.Context(), time.Minute)}, store, catalog
}

func ObservationSnapshot() agents.AgentSnapshot {
	return agents.AgentSnapshot{ID: AgentID, Type: "fixture", Status: agents.AgentReady,
		Server:  agents.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
		Actions: []actions.Action{{Name: "inspect", Status: actions.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}},
	}
}

func ObservationMessage(t *testing.T, kind messages.MessageType, at time.Time, snapshot agents.AgentSnapshot) messages.Message {
	t.Helper()
	message, err := messages.NewMessage(kind, AgentID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	message.CreatedAt = at
	return message
}
