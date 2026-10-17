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
	wantMissing := []actions.Requirement{
		{Kind: actions.SecretRequirement, Name: "CODEX_API_KEY"},
		{Kind: actions.SecretRequirement, Name: "GITHUB_TOKEN"},
	}
	if snapshot.Type != "builder-agent" ||
		snapshot.Actions[0].Status != actions.Unavailable || !slices.Equal(snapshot.Actions[0].MissingRequirements, wantMissing) ||
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
	// Host configuration and a managed secret can share a name without sharing
	// a source or resolution path.
	hostRequirement := actions.Requirement{Kind: actions.ConfigurationRequirement, Name: "CODEX_API_KEY", Sensitive: true}
	executor.SetUnavailable("Missing configuration", hostRequirement)
	mixed := runtime.Describe().Actions[0]
	if !slices.Equal(mixed.MissingRequirements, append([]actions.Requirement{hostRequirement}, wantMissing...)) {
		t.Fatalf("sensitive host configuration was conflated with managed secrets: %+v", mixed)
	}
	if _, err := runtime.Work(t.Context(), actions.ActionCall{ActionName: "build", Input: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("action with required secrets executed without secret configuration")
	}
}

func TestActionValidatesManagedSecretRequirements(t *testing.T) {
	for _, test := range []struct {
		name        string
		requirement actions.Requirement
		declared    []string
		valid       bool
	}{
		{name: "managed secret", requirement: actions.Requirement{Kind: actions.SecretRequirement, Name: "TOKEN"}, declared: []string{"TOKEN"}, valid: true},
		{name: "undeclared secret", requirement: actions.Requirement{Kind: actions.SecretRequirement, Name: "TOKEN"}},
		{name: "invalid secret name", requirement: actions.Requirement{Kind: actions.SecretRequirement, Name: "../TOKEN"}, declared: []string{"TOKEN"}},
		{name: "sensitive host configuration", requirement: actions.Requirement{Kind: actions.ConfigurationRequirement, Name: "TOKEN", Sensitive: true}, valid: true},
		{name: "sensitivity flag on managed secret", requirement: actions.Requirement{Kind: actions.SecretRequirement, Name: "TOKEN", Sensitive: true}, declared: []string{"TOKEN"}},
		{name: "sensitivity flag on executable", requirement: actions.Requirement{Kind: actions.ExecutableRequirement, Name: "git", Sensitive: true}},
		{name: "unknown kind", requirement: actions.Requirement{Kind: "other", Name: "TOKEN"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := actions.Action{Name: "build", Status: actions.Unavailable,
				RequiredSecrets: test.declared, MissingRequirements: []actions.Requirement{test.requirement},
				InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
			if err := definition.Validate(); (err == nil) != test.valid {
				t.Fatalf("unexpected requirement validation: %v", err)
			}
		})
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
