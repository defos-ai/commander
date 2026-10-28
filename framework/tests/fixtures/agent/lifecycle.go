package fixtures

import (
	"context"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func StateAgent(t *testing.T, actions ...action.Executor) *agent.Agent {
	t.Helper()
	noHeartbeat := time.Duration(0)
	a, err := agent.NewAgent(agent.AgentConfig{Type: "fixture", Actions: actions,
		Server:            agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
		HeartbeatInterval: &noHeartbeat, ArtifactFileStore: fakes.UnusedArtifacts{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func NextAnnouncement(t *testing.T, ctx context.Context, bus *fakes.AnnouncementBus) fakes.Announcement {
	t.Helper()
	select {
	case publication := <-bus.Published():
		return publication
	case <-ctx.Done():
		t.Fatal("expected agent announcement")
		return fakes.Announcement{}
	}
}
