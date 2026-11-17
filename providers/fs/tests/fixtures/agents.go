package fixtures

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
)

func OpenAgentStore(t *testing.T, root string) *fsagents.AgentFsStore {
	t.Helper()
	store, err := fsagents.NewAgentFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func Agent(index int, observed time.Time) agents.Agent {
	return agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{
			ID: fmt.Sprintf("browser-agent-%08x", index), Type: "browser-agent", Status: agents.AgentReady,
			Config: json.RawMessage(`{}`),
		},
		ObservedAt: observed, LastSeen: observed.Add(time.Second), LastHeartbeat: observed,
		LeaseExpiresAt: observed.Add(agents.DefaultHeartbeatTimeout),
	}
}
