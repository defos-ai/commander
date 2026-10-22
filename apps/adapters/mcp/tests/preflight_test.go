package mcp_test

import (
	"slices"
	"testing"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func assertMissionPreflight(t *testing.T, a *api, op operationDetail, expectedActions int) {
	t.Helper()
	var inventory struct {
		Agents []agents.Agent `json:"agents"`
	}
	a.call("list_agents", object{}, &inventory)

	if op.Preflight == nil || !op.Preflight.Ready || len(op.Preflight.Actions) != expectedActions {
		t.Fatalf("mission readiness missing: %+v", op)
	}
	for _, coverage := range op.Preflight.Actions {
		if op.Mission.Name == "review-website-no-browser" && (coverage.ActionName == "capture-page" || coverage.ActionName == "check-accessibility") {
			t.Fatalf("browser-free preflight requires rendered work: %+v", coverage)
		}
		expectedIDs := []string{}
		for _, agent := range inventory.Agents {
			if string(agent.Type) == expectedExecutorType(coverage.ActionName) {
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
	a.call("describe_action", object{"action_name": "capture-page"}, &definition)
	if len(definition.MissingRequirements) != 0 {
		t.Fatalf("machine requirements leaked into the shared catalog: %+v", definition)
	}
	var listed struct {
		Operations []operationDetail `json:"operations"`
	}
	a.call("list_operations", object{}, &listed)
	if len(listed.Operations) != 1 || listed.Operations[0].Preflight == nil || !listed.Operations[0].Preflight.Ready {
		t.Fatalf("operation summary omitted preflight: %+v", listed)
	}
}
