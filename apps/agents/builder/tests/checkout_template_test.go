package builder_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

func TestCheckoutTemplateRetainsIndependentWorkspaces(t *testing.T) {
	source, revision := templateFixture(t)
	root := t.TempDir()
	executor := localCheckout(t, root, source)
	first := runCheckout(t, executor)
	second := runCheckout(t, executor)
	if first.WorkspaceID == second.WorkspaceID {
		t.Fatal("independent checkouts reused a workspace")
	}
	for _, output := range []builderactions.CheckoutTemplateOutput{first, second} {
		if output.WorkspaceID == "" || filepath.Base(output.WorkspaceID) != output.WorkspaceID || output.WorkspaceID == "." || output.WorkspaceID == ".." {
			t.Fatalf("workspace identifier is not an opaque directory name: %q", output.WorkspaceID)
		}
		if output.TemplateRevision != revision {
			t.Fatalf("revision = %q, want %q", output.TemplateRevision, revision)
		}
		workspace := filepath.Join(root, output.WorkspaceID)
		content, err := os.ReadFile(filepath.Join(workspace, "source", "README.md"))
		if err != nil || string(content) != "Local example-agent fixture.\n" {
			t.Fatalf("checked-out source = %q, %v", content, err)
		}
		if _, err := os.Stat(filepath.Join(workspace, ".home")); !os.IsNotExist(err) {
			t.Fatalf("temporary Git home remained after success: %v", err)
		}
		if encoded, _ := json.Marshal(output); strings.Contains(string(encoded), root) {
			t.Fatal("checkout result exposed the host workspace path")
		}
	}
}

func TestConcurrentCheckoutsKeepSourceIndependent(t *testing.T) {
	source, _ := templateFixture(t)
	root := t.TempDir()
	executor := localCheckout(t, root, source)
	type outcome struct {
		result action.Result
		err    error
	}
	completed := make(chan outcome, 2)
	for range 2 {
		go func() {
			result, err := executor.Run(t.Context(), json.RawMessage(`{}`))
			completed <- outcome{result, err}
		}()
	}
	var outputs []builderactions.CheckoutTemplateOutput
	for range 2 {
		got := <-completed
		if got.err != nil {
			t.Errorf("concurrent checkout: %v", got.err)
			continue
		}
		var output builderactions.CheckoutTemplateOutput
		if err := json.Unmarshal(got.result.Output, &output); err != nil {
			t.Errorf("decode concurrent checkout: %v", err)
			continue
		}
		outputs = append(outputs, output)
	}
	if len(outputs) != 2 {
		t.FailNow()
	}
	if outputs[0].WorkspaceID == outputs[1].WorkspaceID {
		t.Fatal("concurrent checkouts shared one workspace")
	}
	if err := os.WriteFile(filepath.Join(root, outputs[0].WorkspaceID, "source", "README.md"), []byte("Edited by the first task"), 0600); err != nil {
		t.Fatal(err)
	}
	other, err := os.ReadFile(filepath.Join(root, outputs[1].WorkspaceID, "source", "README.md"))
	if err != nil || string(other) != "Local example-agent fixture.\n" {
		t.Fatalf("editing one workspace changed another: %q, %v", other, err)
	}
}

func TestCheckoutTemplateRejectsCallerControlledSourceAndPaths(t *testing.T) {
	root := t.TempDir()
	executor := localCheckout(t, root, "")
	for _, input := range []string{
		`{"url":"https://untrusted.invalid/template"}`,
		`{"path":"../../outside"}`,
		`{"workspace_id":"../../outside"}`,
		`{"revision":"--upload-pack=untrusted"}`,
		`null`, `[]`, `{} {}`,
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := executor.Run(t.Context(), json.RawMessage(input)); err == nil || !strings.Contains(err.Error(), "input") {
				t.Fatalf("invalid checkout input was not rejected by input validation: %v", err)
			}
			assertEmptyWorkspaceRoot(t, root)
		})
	}
}

func TestCheckoutTemplateGitDiscoveryAndRediscovery(t *testing.T) {
	source, _ := templateFixture(t)
	root := t.TempDir()
	executor := localCheckout(t, root, source)
	status, err := executor.Discover(t.Context(), &environment.Local{}, &environment.DiscoveryScope{Paths: []string{}})
	if err != nil || status != action.Unavailable {
		t.Fatalf("empty discovery scope: status=%s error=%v", status, err)
	}
	metadata := executor.Action()
	if metadata.StatusReason == "" || len(metadata.MissingRequirements) != 1 || metadata.MissingRequirements[0] != (actionmodel.Requirement{Kind: actionmodel.ExecutableRequirement, Name: "git"}) {
		t.Fatalf("missing Git dependency was not reported: %+v", metadata)
	}
	if _, err := executor.Run(t.Context(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("rediscovery retained the previous Git executable")
	}
	assertEmptyWorkspaceRoot(t, root)
	executor.discover(t)
	if len(executor.Action().MissingRequirements) != 0 {
		t.Fatal("successful rediscovery retained missing requirements")
	}
	runCheckout(t, executor)
}

func TestCheckoutTemplateFailureRemovesWorkspace(t *testing.T) {
	root := t.TempDir()
	executor := localCheckout(t, root, filepath.Join(t.TempDir(), "missing-repository"))
	if _, err := executor.Run(t.Context(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("nonexistent repository checkout succeeded")
	}
	assertEmptyWorkspaceRoot(t, root)
}

func TestCheckoutTemplateAlreadyCancelled(t *testing.T) {
	root := t.TempDir()
	executor := localCheckout(t, root, filepath.Join(t.TempDir(), "missing-repository"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := executor.Run(ctx, json.RawMessage(`{}`)); err == nil {
		t.Fatal("cancelled checkout succeeded")
	}
	assertEmptyWorkspaceRoot(t, root)
}

type checkoutFixture struct {
	*builderactions.CheckoutTemplate
	scope *environment.DiscoveryScope
}

func localCheckout(t *testing.T, root, source string) *checkoutFixture {
	t.Helper()
	return configuredCheckout(t, root, gitFixtureConfig{Source: source})
}

func configuredCheckout(t *testing.T, root string, config gitFixtureConfig) *checkoutFixture {
	t.Helper()
	executor, err := builderactions.NewCheckoutTemplate(root)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &checkoutFixture{CheckoutTemplate: executor, scope: gitFixtureScope(t, config)}
	fixture.discover(t)
	return fixture
}

func (f *checkoutFixture) discover(t *testing.T) {
	t.Helper()
	status, err := f.Discover(t.Context(), &environment.Local{}, f.scope)
	if err != nil || status != action.Ready {
		t.Fatalf("Git discovery: status=%s error=%v", status, err)
	}
}

func runCheckout(t *testing.T, executor action.Executor) builderactions.CheckoutTemplateOutput {
	t.Helper()
	result, err := executor.Run(t.Context(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var output builderactions.CheckoutTemplateOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if len(result.NextActions) != 0 || len(result.Artifacts) != 0 {
		t.Fatal("checkout should only prepare source, without proposing unimplemented work")
	}
	return output
}

func assertEmptyWorkspaceRoot(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed checkout left %d workspace entries", len(entries))
	}
}
