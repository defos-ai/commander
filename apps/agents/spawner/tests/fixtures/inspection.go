package fixtures

import (
	"testing"

	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/action"
)

const InspectionImage = "ghcr.io/other/worker-agent@sha256:1111111111111111111111111111111111111111111111111111111111111111"

func InspectionManifest() string {
	return `schema_version: 1
type: worker-agent
version: v1.2.3
dependencies:
  - name: lightpanda
    description: Browser engine used for navigation.
    version: v0.2.0
  - name: hermes
    description: Browser automation CLI.
native:
  windows/arm64:
    asset: worker-agent_windows_arm64.exe
    sha256: "1111111111111111111111111111111111111111111111111111111111111111"
  linux/amd64:
    asset: worker-agent_linux_amd64
    sha256: "2222222222222222222222222222222222222222222222222222222222222222"
docker:
  image: ` + InspectionImage + "\n"
}

func InspectionDockerManifest() string {
	return "schema_version: 1\ntype: worker-agent\nversion: v1.2.3\ndocker:\n  image: " + InspectionImage + "\n"
}

func InspectionResolver(t *testing.T, source *fakes.InspectionSource) *packages.Resolver {
	t.Helper()
	resolver := packages.NewResolver(source)
	t.Cleanup(resolver.CloseIdleConnections)
	return resolver
}

func InspectAction(t *testing.T, resolver *packages.Resolver, kind runtimes.RuntimeKind) action.Executor {
	t.Helper()
	executor, err := spawneractions.NewInspectAgent(resolver, kind)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Discover(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
	return executor
}
