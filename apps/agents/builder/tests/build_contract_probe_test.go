package builder_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
)

func TestBuildAgentInspectsRealRegisteredActions(t *testing.T) {
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, err = filepath.Abs(goExecutable)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	moduleCache, err := exec.CommandContext(ctx, goExecutable, "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal("locate trusted Go module cache:", err)
	}
	framework, err := filepath.Abs("../../../../framework")
	if err != nil {
		t.Fatal(err)
	}
	// Reuse compilation between the two cases, but keep all generated output private.
	cache := t.TempDir()
	for _, mode := range []string{"contracts-real", "contracts-real-missing"} {
		t.Run(mode, func(t *testing.T) {
			root, workspaceID := fixtures.BuildWorkspace(t)
			control := t.TempDir()
			config := fakes.BuildConfig{
				Source: filepath.Join(root, workspaceID, "source"), Mode: mode,
				Started: filepath.Join(control, "started"), Calls: filepath.Join(control, "calls"), Home: filepath.Join(control, "home"),
				ProbeGo: goExecutable, ProbeWorkspace: filepath.Join(control, "go.work"),
				ProbeModuleCache: strings.TrimSpace(string(moduleCache)), ProbeCache: cache,
			}
			// Keep versioned framework graph lookups local too: CI may have no
			// published framework module cached, and this probe runs offline.
			work := fmt.Sprintf("go 1.26.7\n\nuse (\n\t%q\n\t%q\n)\n\nreplace github.com/defos-ai/commander/framework v0.1.0 => %q\n", config.Source, framework, framework)
			if err := os.WriteFile(config.ProbeWorkspace, []byte(work), 0600); err != nil {
				t.Fatal(err)
			}
			scope := fixtures.BuildScope(t, config)
			executor, err := builderactions.NewBuildAgent(root)
			if err != nil {
				t.Fatal(err)
			}
			if status, err := executor.Discover(t.Context(), &environment.Local{}, scope); err != nil || status != action.Ready {
				t.Fatalf("build discovery: status=%s err=%v", status, err)
			}

			result, err := executor.Run(fixtures.BuildContext(t), fixtures.BuildInput(t, workspaceID))

			if mode == "contracts-real-missing" {
				if err == nil || !strings.Contains(err.Error(), "generated action names do not match") || len(result.Output) != 0 || len(result.Artifacts) != 0 || len(result.NextActions) != 0 {
					t.Fatalf("real constructor omitted an action but the build accepted it: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal("inspect real constructor:", err)
				}
				if len(result.Artifacts) != 1 || len(result.Artifacts[0].Files) != 1 {
					t.Fatal("verified real constructor did not produce source")
				}
				files := readBuildArchive(t, result.Artifacts[0].Files[0].Data)
				if !strings.Contains(files["agent.go"], "func NewAgent(") || !strings.Contains(files["agent.go"], "action execution must not run during contract inspection") {
					t.Fatal("artifact did not contain the inspected constructor")
				}
				if strings.Contains(files["go.mod"], "replace ") || strings.Contains(files["go.mod"], root) {
					t.Fatal("test-only workspace wiring leaked into generated source")
				}
			}
			calls, readErr := os.ReadFile(config.Calls)
			probeName := filepath.Base(strings.TrimSpace(string(calls)))
			if readErr != nil || strings.Count(string(calls), "\n") != 7 || !strings.HasPrefix(probeName, "action-contracts-") || !strings.HasSuffix(probeName, ".go") {
				t.Fatalf("real probe did not follow the normal build and release stages: %s (%v)", calls, readErr)
			}
			assertBuildScratchRemoved(t, config)
		})
	}
}
