package builder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
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
			root, workspaceID := buildWorkspace(t)
			control := t.TempDir()
			config := buildFixtureConfig{
				Source: filepath.Join(root, workspaceID, "source"), Mode: mode,
				Started: filepath.Join(control, "started"), Calls: filepath.Join(control, "calls"), Home: filepath.Join(control, "home"),
				ProbeGo: goExecutable, ProbeWorkspace: filepath.Join(control, "go.work"),
				ProbeModuleCache: strings.TrimSpace(string(moduleCache)), ProbeCache: cache,
			}
			work := fmt.Sprintf("go 1.26.7\n\nuse (\n\t%q\n\t%q\n)\n", config.Source, framework)
			if err := os.WriteFile(config.ProbeWorkspace, []byte(work), 0600); err != nil {
				t.Fatal(err)
			}
			scope := buildFixtureScope(t, config)
			executor, err := builderactions.NewBuildAgent(root)
			if err != nil {
				t.Fatal(err)
			}
			if status, err := executor.Discover(t.Context(), &environment.Local{}, scope); err != nil || status != action.Ready {
				t.Fatalf("build discovery: status=%s err=%v", status, err)
			}
			result, err := executor.Run(buildSecretContext(t), buildInput(t, workspaceID))
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

// Only the constructor probe uses real Go. The ordinary fixture checks still
// assert production's isolated environment before this test-only delegation.
func runRealContractProbe(args []string, config buildFixtureConfig) error {
	definitions := buildRequestedActions()
	if config.Mode == "contracts-real-missing" {
		definitions = definitions[:1]
	}
	encoded, err := json.Marshal(definitions)
	if err != nil {
		return err
	}
	source := fmt.Sprintf(`package generated

import (
	"context"
	"encoding/json"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

const AgentType = %q

func NewAgent(config agent.AgentConfig) (*agent.Agent, error) {
	var definitions []actionmodel.Definition
	if err := json.Unmarshal([]byte(%q), &definitions); err != nil {
		return nil, err
	}
	config.Type = AgentType
	for _, definition := range definitions {
		config.Actions = append(config.Actions, &contractAction{Base: action.NewBase(actionmodel.Action{
			Name: definition.Name, Description: definition.Description,
			InputSchema: definition.InputSchema, OutputSchema: definition.OutputSchema,
			RequiredSecrets: definition.RequiredSecrets,
		})})
	}
	return agent.NewAgent(config)
}

type contractAction struct { *action.Base }

func (*contractAction) Discover(context.Context, environment.Environment, *environment.DiscoveryScope) (action.Status, error) {
	panic("discovery must not run during contract inspection")
}

func (*contractAction) Run(context.Context, json.RawMessage) (action.Result, error) {
	panic("action execution must not run during contract inspection")
}
`, buildTestAgentType, string(encoded))
	if err := os.WriteFile(filepath.Join(config.Source, "agent.go"), []byte(source), 0600); err != nil {
		return err
	}
	module := "module example.invalid/fixture\n\ngo 1.26.7\n\nrequire github.com/defos-ai/commander/framework v0.1.0\n"
	if err := os.WriteFile(filepath.Join(config.Source, "go.mod"), []byte(module), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, config.ProbeGo, args...)
	command.Dir = config.Source
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GOWORK", "GOMODCACHE", "GOCACHE", "GOPROXY", "GOSUMDB", "GOTOOLCHAIN", "CGO_ENABLED", "GOFLAGS":
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "GOWORK="+config.ProbeWorkspace,
		"GOMODCACHE="+config.ProbeModuleCache, "GOCACHE="+config.ProbeCache,
		"GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOFLAGS=")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}
