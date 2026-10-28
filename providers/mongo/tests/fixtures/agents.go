package fixtures

import (
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
)

func Agent(index int, heartbeat time.Time) agents.Agent {
	return agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{ID: fmt.Sprintf("browser-agent-%08x", index), Type: "browser-agent", Status: agents.AgentReady},
		LastSeen:      time.Date(2026, 9, 30, 12, 0, 1, 123456789, time.UTC),
		LastHeartbeat: heartbeat,
	}
}
