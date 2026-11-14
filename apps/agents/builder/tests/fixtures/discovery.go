package fixtures

import (
	"testing"

	builder "github.com/defos-ai/commander/apps/agents/builder"
	"github.com/defos-ai/commander/framework/agent"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
)

func DiscoveryBuilder(t *testing.T) *agent.Agent {
	t.Helper()
	files, err := fsartifacts.NewArtifactFileFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	instance, err := builder.NewAgent(agent.AgentConfig{
		ArtifactFileStore: files,
		Server:            agent.ServerInfo{Hostname: "builder-discovery-fixture", IPAddress: "127.0.0.1"},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return instance
}
