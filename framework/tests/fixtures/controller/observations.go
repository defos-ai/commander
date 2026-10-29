package fixtures

import (
	"encoding/json"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func ObservationSnapshot() agents.AgentSnapshot {
	return agents.AgentSnapshot{ID: AgentID, Type: "fixture", Status: agents.AgentReady,
		Server:  agents.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
		Actions: []actions.Action{{Name: "inspect", Status: actions.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}},
	}
}
