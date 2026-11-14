package fixtures

import (
	"encoding/json"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func AgentWithDependencies() agents.Agent {
	now := time.Now().UTC()
	return agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{
			ID: "browser-agent-00000001", Type: "browser-agent", Status: agents.AgentReady,
			Server: agents.ServerInfo{IPAddress: "198.51.100.10", Hostname: "native-browser-host"},
			Dependencies: []agents.Dependency{
				{Name: "lightpanda", Description: "Browser runtime for the execution host.", Version: "v1.2.3"},
				{Name: "helper", Description: "Optional helper tool."},
			},
			Actions: []actions.Action{
				{Name: "browse", Status: actions.Unavailable, StatusReason: "Install the browser runtime",
					MissingRequirements: []actions.Requirement{{Kind: actions.ExecutableRequirement, Name: "lightpanda"}},
					InputSchema:         json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)},
				{Name: "inspect", Status: actions.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)},
			},
		},
		ObservedAt: now, LastSeen: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Hour),
	}
}
