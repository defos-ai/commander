package mcp_test

import (
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func assertMissionPreflight(t *testing.T, a *fixtures.Client, op fixtures.OperationDetail, expectedActions int) {
	t.Helper()
	var inventory struct {
		Agents []agents.Agent `json:"agents"`
	}
	a.Call("list_agents", fixtures.Object{}, &inventory)

	if op.Preflight == nil || !op.Preflight.Ready || len(op.Preflight.Actions) != expectedActions {
		t.Fatalf("mission readiness missing: %+v", op)
	}
	for _, coverage := range op.Preflight.Actions {
		expectedIDs := []string{}
		for _, agent := range inventory.Agents {
			if agent.Type == "example-agent" {
				expectedIDs = append(expectedIDs, agent.ID)
			}
		}
		slices.Sort(expectedIDs)
		readyIDs := slices.Clone(coverage.ReadyAgentIDs)
		slices.Sort(readyIDs)
		if !slices.Equal(readyIDs, expectedIDs) || len(coverage.UnavailableAgents) != 0 {
			t.Fatalf("ready action lost agent coverage: %+v", coverage)
		}
	}
	var definition actions.Action
	a.Call("describe_action", fixtures.Object{"action_name": "analyze-text"}, &definition)
	if len(definition.MissingRequirements) != 0 {
		t.Fatalf("machine requirements leaked into the shared catalog: %+v", definition)
	}
	var listed struct {
		Operations []fixtures.OperationDetail `json:"operations"`
	}
	a.Call("list_operations", fixtures.Object{}, &listed)
	if len(listed.Operations) != 1 || listed.Operations[0].Preflight == nil || !listed.Operations[0].Preflight.Ready {
		t.Fatalf("operation summary omitted preflight: %+v", listed)
	}
}
