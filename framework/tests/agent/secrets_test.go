package agent_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func TestSecretDeclarationsSurviveDiscoveryAndIndependentSnapshots(t *testing.T) {
	definition := actions.Action{Name: "build", RequiredSecrets: []string{"CODEX_API_KEY", "GITHUB_TOKEN"},
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	executor := &secretAction{Base: action.NewBase(definition)}
	definition.RequiredSecrets[0] = "CHANGED"
	executor.SetUnavailable("Missing configuration", actions.Requirement{
		Kind: actions.ConfigurationRequirement, Name: "CODEX_API_KEY", Secret: true,
	})
	runtime, err := agent.NewAgent(agent.AgentConfig{Type: "builder-agent",
		Actions: []action.Executor{executor}, ArtifactFileStore: unusedArtifacts{},
		Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Describe()
	want := []string{"CODEX_API_KEY", "GITHUB_TOKEN"}
	if snapshot.Type != "builder-agent" ||
		snapshot.Actions[0].Status != actions.Unavailable || len(snapshot.Actions[0].MissingRequirements) != 2 ||
		!slices.Equal(snapshot.Actions[0].RequiredSecrets, want) {
		t.Fatalf("discovery lost stable secret declarations: %+v", snapshot)
	}
	snapshot.Actions[0].RequiredSecrets[0] = "CHANGED"
	if got := runtime.Describe().Actions[0].RequiredSecrets; !slices.Equal(got, want) {
		t.Fatalf("reader mutated agent secret declarations: %v", got)
	}
	readAction := executor.Action()
	readAction.RequiredSecrets[0] = "CHANGED"
	if got := executor.Action().RequiredSecrets; !slices.Equal(got, want) {
		t.Fatalf("reader mutated action secret declarations: %v", got)
	}
	record := agents.Agent{AgentSnapshot: runtime.Describe()}
	cloned := record.Clone()
	cloned.Actions[0].RequiredSecrets[0] = "CHANGED"
	if !slices.Equal(record.Actions[0].RequiredSecrets, want) {
		t.Fatal("cloned inventory shares secret declarations")
	}
	if _, err := runtime.Work(t.Context(), actions.ActionCall{ActionName: "build", Input: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("action with required secrets executed without secret configuration")
	}
}

func TestInvalidSecretDeclarationsRejectedBeforeDiscovery(t *testing.T) {
	for _, names := range [][]string{{""}, {"API_TOKEN", "API_TOKEN"}, {"../TOKEN"}, {"API TOKEN"}, {"*"}, {"ТOKEN"}, {strings.Repeat("A", 129)}} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			definition := actions.Action{Name: "build", RequiredSecrets: names, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
			if err := definition.Validate(); err == nil {
				t.Fatal("invalid action secret declarations accepted")
			}
			_, err := agent.NewAgent(agent.AgentConfig{Type: "builder-agent",
				Actions: []action.Executor{&secretAction{Base: action.NewBase(definition)}}, ArtifactFileStore: unusedArtifacts{},
				Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
			})
			if err == nil {
				t.Fatal("agent accepted invalid declarations")
			}
			definition.InputSchema, definition.OutputSchema = nil, nil
			_, err = action.NewFunc(action.FuncConfig[struct{}, struct{}]{Action: definition,
				Run: func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil },
			})
			if err == nil {
				t.Fatal("function action accepted invalid declarations")
			}
		})
	}
}

type secretAction struct{ *action.Base }

func (*secretAction) Run(context.Context, json.RawMessage) (action.Result, error) {
	return action.JSONResult(struct{}{})
}
