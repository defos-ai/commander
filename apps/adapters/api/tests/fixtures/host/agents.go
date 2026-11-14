package host

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
)

func NewAgentInventory(t *testing.T, root string) *fsagents.AgentFsStore {
	t.Helper()
	store, err := fsagents.NewAgentFsStore(filepath.Join(root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func AgentDependencyRecord() agents.Agent {
	now := time.Now().UTC().Add(-time.Second)
	return agents.Agent{AgentSnapshot: agents.AgentSnapshot{
		ID: "browser-agent-00000001", Type: "browser-agent", Status: agents.AgentReady,
		Server: agents.ServerInfo{IPAddress: "127.0.0.1", Hostname: "dependency-fixture"},
		Dependencies: []agents.Dependency{
			{Name: "lightpanda", Description: "Install the browser runtime for the execution host.", Version: "v1.2.3"},
			{Name: "helper", Description: "Optional helper runtime."},
		},
		Actions: []actions.Action{{Name: "browse", Status: actions.Unavailable,
			MissingRequirements: []actions.Requirement{{Kind: actions.ExecutableRequirement, Name: "lightpanda"}},
			InputSchema:         json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
		}},
		Config: json.RawMessage(`{}`),
	}, ObservedAt: now, LastSeen: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Hour)}
}
