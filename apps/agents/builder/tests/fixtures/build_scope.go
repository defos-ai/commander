package fixtures

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/environment"
)

func BuildScope(t *testing.T, config fakes.BuildConfig) *environment.DiscoveryScope {
	t.Helper()
	config.Protocol = fakes.BuildProtocol{
		Secret: BuildSecret, Instructions: BuildInstructions, AgentType: BuildAgentType,
		Binary: BuildBinary, ReleaseWorkflow: ReleaseWorkflow, Actions: BuildRequestedActions(),
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, name := range []string{"codex", "go"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(bin, name)
		if runtime.GOOS == "windows" {
			if err := copyExecutable(self, path); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Link(self, path); err != nil {
			if err := copyExecutable(self, path); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "build-fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return &environment.DiscoveryScope{Paths: []string{bin}}
}
