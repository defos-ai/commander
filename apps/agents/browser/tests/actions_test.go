package browser_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/defos-ai/commander/apps/agents/browser/actions"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

func TestBrowserDiscoveryRespectsExplicitScopeAndRediscovery(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "browser-fixture")
	if err := os.WriteFile(executable, []byte("fixture executable, never launched"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHROMIUM_PATH", executable)
	t.Setenv("CHROMIUM_NO_SANDBOX", "false")
	for _, executor := range []action.Executor{actions.NewCapturePage(), actions.NewCheckAccessibility()} {
		t.Run(executor.Action().Name, func(t *testing.T) {
			status, err := executor.Discover(t.Context(), &environment.Local{}, nil)
			if err != nil || status != action.Ready {
				t.Fatalf("configured browser discovery: status=%s error=%v", status, err)
			}
			status, err = executor.Discover(t.Context(), &environment.Local{}, &environment.DiscoveryScope{Paths: []string{}})

			if err != nil || status != action.Unavailable || executor.Action().Status != action.Unavailable || executor.Action().StatusReason == "" {
				t.Fatalf("restricted browser discovery: action=%+v error=%v", executor.Action(), err)
			}
			missing := executor.Action().MissingRequirements
			if len(missing) != 1 || missing[0] != (actionmodel.Requirement{Kind: actionmodel.ExecutableRequirement, Name: "chromium"}) {
				t.Fatalf("missing structured dependency: %+v", missing)
			}
			missing[0].Name = "caller mutation"
			if executor.Action().MissingRequirements[0].Name != "chromium" {
				t.Fatal("action snapshot shares mutable requirements")
			}
			status, err = executor.Discover(t.Context(), &environment.Local{}, nil)
			if err != nil || status != action.Ready || len(executor.Action().MissingRequirements) != 0 {
				t.Fatalf("rediscovery retained missing requirements: %+v, %v", executor.Action(), err)
			}
		})
	}
}

func TestBrowserDiscoveryReportsInvalidConfigurationAsRequirement(t *testing.T) {
	t.Setenv("CHROMIUM_PATH", "relative/browser")
	executor := actions.NewCapturePage()

	status, err := executor.Discover(t.Context(), &environment.Local{}, nil)

	missing := executor.Action().MissingRequirements
	if err != nil || status != action.Unavailable || len(missing) != 1 || missing[0] != (actionmodel.Requirement{Kind: actionmodel.ConfigurationRequirement, Name: "CHROMIUM_PATH"}) {
		t.Fatalf("invalid configuration was not reported as a readiness gap: %+v, %v", executor.Action(), err)
	}
}
