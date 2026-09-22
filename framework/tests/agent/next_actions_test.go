package agents_test

import (
	"context"
	"encoding/json"
	"testing"

	"commander/framework/agent"
	agentaction "commander/framework/agent/action"
	actionmodel "commander/framework/domain/actions"
	artifactproviders "commander/providers/artifact"
)

func TestAgentPreservesNextActionsWithInlineAndStoredOutput(t *testing.T) {
	for _, limit := range []int{0, 100000} {
		t.Run(map[bool]string{true: "stored", false: "inline"}[limit == 0], func(t *testing.T) {
			// Arrange: typed action proposes two actions independently of its JSON output.
			storage, err := artifactproviders.OpenStorage(t.Context(), t.TempDir(), artifactproviders.StorageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = storage.Close() })
			actions := []agentaction.NextAction{
				{ActionName: "scan-ports", Input: json.RawMessage(`{"target":"192.0.2.1"}`), Reason: "A record"},
				{ActionName: "scan-ports", Input: json.RawMessage(`{"target":"192.0.2.2"}`)},
			}
			fixture, err := agentaction.NewFunc(agentaction.FuncConfig[struct{}, struct{}]{
				Action:      actionmodel.Action{Name: "lookup"},
				Run:         func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil },
				NextActions: func(context.Context, struct{}, struct{}) ([]agentaction.NextAction, error) { return actions, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			worker, err := agent.NewAgent(agent.AgentConfig{Actions: []agentaction.Executor{fixture}, ArtifactStore: storage.ArtifactStore, InlineOutputLimit: &limit})
			if err != nil {
				t.Fatal(err)
			}
			if err := worker.DiscoverActions(t.Context(), nil); err != nil {
				t.Fatal(err)
			}

			// Act: use the public agent execution path and serialize its wire result.
			output, err := worker.Work(t.Context(), agent.ActionCall{ActionName: "lookup", Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			actions[0].Input[0] = '[' // returned proposals must not alias the action's buffer
			wire, err := json.Marshal(output)
			if err != nil {
				t.Fatal(err)
			}
			var received agent.ActionOutput
			if err := agent.Decode(wire, &received); err != nil {
				t.Fatal(err)
			}
			data, err := received.ResolveOutput(t.Context(), storage.ArtifactStore)

			// Assert: proposals remain visible even when the action output is an artifact.
			if err != nil || string(data) != `{}` || len(received.NextActions) != 2 || received.NextActions[0].Reason != "A record" {
				t.Fatalf("lost output or proposals: %+v, %s, %v", received, data, err)
			}
			if (received.OutputArtifact != nil) != (limit == 0) {
				t.Fatalf("unexpected output storage: %+v", received)
			}
		})
	}
}

func TestActionResultRejectsMalformedNextActions(t *testing.T) {
	for _, action := range []agentaction.NextAction{
		{ActionName: " "}, {ActionName: "scan-ports", AgentID: "invalid"},
		{ActionName: "scan-ports", Input: json.RawMessage(`[]`)},
		{ActionName: "scan-ports", Input: json.RawMessage(`null`)},
	} {
		// Arrange: validate inbound result proposals using the shared wire contract.
		output := agent.ActionOutput{ActionName: "lookup", Output: json.RawMessage(`{}`), NextActions: []agentaction.NextAction{action}}
		// Act.
		err := output.Validate()
		// Assert.
		if err == nil {
			t.Fatalf("accepted invalid proposal: %+v", action)
		}
	}
}
