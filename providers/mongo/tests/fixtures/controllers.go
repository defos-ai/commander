package fixtures

import (
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/domain/controllers"
)

func Controller(index int) controllers.Controller {
	heartbeat := time.Date(2026, 10, 6, 12, 0, 0, 123456789, time.UTC)
	return controllers.Controller{
		ID: fmt.Sprintf("default-controller-%08x", index), Type: "default", Status: controllers.Ready,
		Server: controllers.ServerInfo{Hostname: "worker-1", OS: "linux", Arch: "amd64"}, Version: "v1", AgentSelector: "*", RuleSelector: "mission-rules",
		StartedAt: heartbeat.Add(-time.Minute), LastHeartbeat: heartbeat, LeaseExpiresAt: heartbeat.Add(45 * time.Second),
	}
}
