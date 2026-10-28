package fixtures

import (
	"time"

	"github.com/defos-ai/commander/framework/domain/controllers"
)

func Registration() controllers.Controller {
	stamp := time.Date(2026, 10, 6, 12, 0, 0, 123456789, time.UTC)
	return controllers.Controller{
		ID: "default-controller-0123abcd", Type: "default", Status: controllers.Ready,
		Server:  controllers.ServerInfo{Hostname: "worker-1", OS: "linux", Arch: "amd64"},
		Version: "v1", AgentSelector: "*", RuleSelector: "mission-rules", StartedAt: stamp.Add(-time.Minute),
		LastHeartbeat: stamp, LeaseExpiresAt: stamp.Add(45 * time.Second),
	}
}
