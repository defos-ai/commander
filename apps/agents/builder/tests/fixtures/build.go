package fixtures

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
)

func BuildExecutor(t *testing.T, root, workspaceID, mode string) (*builderactions.BuildAgent, fakes.BuildConfig, *environment.DiscoveryScope) {
	t.Helper()
	control := t.TempDir()
	config := fakes.BuildConfig{Source: filepath.Join(root, workspaceID, "source"), Mode: mode,
		Started: filepath.Join(control, "started"), Calls: filepath.Join(control, "calls"), Home: filepath.Join(control, "home")}
	scope := BuildScope(t, config)
	executor, err := builderactions.NewBuildAgent(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := executor.Discover(t.Context(), &environment.Local{}, scope)
	if err != nil || status != action.Ready {
		t.Fatalf("build discovery: status=%s err=%v", status, err)
	}
	if names := executor.Action().RequiredSecrets; len(names) != 1 || names[0] != "CODEX_API_KEY" {
		t.Fatalf("build secret declaration: %v", names)
	}
	return executor, config, scope
}

func BuildWorkspace(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	const id = "checkout-123456"
	source := filepath.Join(root, id, "source")
	WriteBuildFile(t, source, "README.md", "Local source fixture\n")
	WriteBuildFile(t, source, "go.mod", "module example.invalid/fixture\n\ngo 1.26.7\n")
	return root, id
}

func WriteBuildFile(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func BuildInput(t *testing.T, workspaceID string) json.RawMessage {
	t.Helper()
	input, err := json.Marshal(builderactions.BuildAgentInput{WorkspaceID: workspaceID, Instructions: BuildInstructions, Actions: BuildRequestedActions()})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func BuildContext(t *testing.T) context.Context {
	t.Helper()
	return action.WithSecrets(t.Context(), fakes.BuildSecrets(BuildSecret))
}
