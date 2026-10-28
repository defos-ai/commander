package fixtures

import (
	"fmt"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	agentfs "github.com/defos-ai/commander/providers/fs/agents"
)

func OpenAgentStore(t *testing.T, root string) *agentfs.AgentFsStore {
	t.Helper()
	store, err := agentfs.NewAgentFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func Agent(number int, observed time.Time) agents.Agent {
	return agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{
			ID: fmt.Sprintf("browser-agent-%08d", number), Type: "browser-agent", Status: agents.AgentReady,
			Server: agents.ServerInfo{IPAddress: "192.0.2.1", Hostname: "worker-1"}, Config: []byte(`{}`),
		},
		ObservedAt: observed, LastSeen: observed.Add(time.Second), LastHeartbeat: observed,
	}
}
