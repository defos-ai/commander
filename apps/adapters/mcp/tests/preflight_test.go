package mcp_test

import (
	"slices"
	"testing"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestMissionPreflightReportsAgentReadiness(t *testing.T) {
	a := connect(t)
	var inventory struct {
		Agents []agents.Agent `json:"agents"`
	}
	a.call("list_agents", object{}, &inventory)
	url := website(t)

	op := a.start(url)

	if op.Preflight == nil || !op.Preflight.Ready || len(op.Preflight.Actions) != 6 {
		t.Fatalf("mission readiness missing: %+v", op)
	}
	for _, coverage := range op.Preflight.Actions {
		if len(coverage.ReadyAgentIDs) != 2 || len(coverage.UnavailableAgents) != 0 {
			t.Fatalf("ready action lost agent coverage: %+v", coverage)
		}
		for _, agent := range inventory.Agents {
			if !slices.Contains(coverage.ReadyAgentIDs, agent.ID) {
				t.Fatalf("preflight omits ready agent %s: %+v", agent.ID, coverage)
			}
		}
	}
	var definition actions.Action
	a.call("describe_action", object{"action_name": "capture-page"}, &definition)
	if len(definition.MissingRequirements) != 0 {
		t.Fatalf("machine requirements leaked into the shared catalog: %+v", definition)
	}
	queued := a.task(op.ID, "check-seo", url)
	finished := a.wait(op.ID, allTasksSucceeded)
	if !finished.Preflight.Ready || len(finished.Tasks) != 1 || finished.Tasks[0].ID != queued.ID {
		t.Fatalf("ready mission did not execute queued work: %+v", finished)
	}
	for _, coverage := range op.Preflight.Actions {
		if coverage.ActionName == "check-seo" && !slices.Contains(coverage.ReadyAgentIDs, finished.Tasks[0].AgentID) {
			t.Fatalf("controller selected an agent outside ready coverage: %+v", finished.Tasks[0])
		}
	}
	var listed struct {
		Operations []operations.Operation `json:"operations"`
	}
	a.call("list_operations", object{}, &listed)
	if len(listed.Operations) != 1 || listed.Operations[0].Preflight == nil || !listed.Operations[0].Preflight.Ready {
		t.Fatalf("operation summary omitted preflight: %+v", listed)
	}
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": "Ready agent checked SEO", "findings": []object{}}}, nil)
}
