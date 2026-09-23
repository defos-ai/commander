package mcp_test

import (
	"context"
	"testing"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/operations"
)

func TestMissionPreflightWaitsForRequirementsAndResumesAfterDiscovery(t *testing.T) {
	a := connect(t)
	var inventory struct {
		Agents []agents.Agent `json:"agents"`
	}
	a.call("list_agents", object{}, &inventory)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), 10*time.Second)
		defer cancel()
		cleanup := *a
		cleanup.ctx = ctx
		for _, agent := range inventory.Agents {
			cleanup.call("discover_actions", object{"agent_id": agent.ID}, nil)
		}
	})
	for _, agent := range inventory.Agents {
		a.call("discover_actions", object{"agent_id": agent.ID, "scope": object{"paths": []string{}}}, nil)
	}

	url := website(t)
	op := a.start(url)
	if op.Status != operations.Waiting || op.Preflight == nil || op.Preflight.Ready || len(op.Preflight.Actions) != 6 {
		t.Fatalf("mission did not wait for browser dependencies: %+v", op)
	}
	for _, coverage := range op.Preflight.Actions {
		if coverage.ActionName != "capture-page" && coverage.ActionName != "check-accessibility" {
			if len(coverage.ReadyAgentIDs) != 2 {
				t.Fatalf("ready action lost agent coverage: %+v", coverage)
			}
			continue
		}
		if len(coverage.ReadyAgentIDs) != 0 || len(coverage.UnavailableAgents) != 2 {
			t.Fatalf("missing browser candidates: %+v", coverage)
		}
		for _, candidate := range coverage.UnavailableAgents {
			if len(candidate.MissingRequirements) != 1 || candidate.MissingRequirements[0] != (actions.Requirement{Kind: actions.ExecutableRequirement, Name: "chromium"}) {
				t.Fatalf("missing structured browser requirement: %+v", candidate)
			}
		}
	}
	var summaries struct {
		Agents []agents.Agent `json:"agents"`
	}
	a.call("list_agents", object{}, &summaries)
	for _, agent := range summaries.Agents {
		for _, action := range agent.Actions {
			if action.Name == "capture-page" && len(action.MissingRequirements) != 1 {
				t.Fatalf("summary dropped requirements: %+v", action)
			}
		}
	}
	var definition actions.Action
	a.call("describe_action", object{"action_name": "capture-page"}, &definition)
	if len(definition.MissingRequirements) != 0 {
		t.Fatalf("machine requirements leaked into the shared catalog: %+v", definition)
	}

	task := a.task(op.ID, "check-seo", url)
	blocked := a.get(op.ID)
	if blocked.Status != operations.Waiting || blocked.Tasks[0].Status != operations.TaskQueued {
		t.Fatalf("work started despite failed preflight: %+v", blocked)
	}
	a.call("discover_actions", object{"agent_id": inventory.Agents[0].ID}, nil)
	finished := a.wait(op.ID, allTasksSucceeded)
	if !finished.Preflight.Ready || finished.Preflight.Version <= op.Preflight.Version || len(finished.Tasks) != 1 || finished.Tasks[0].ID != task.ID {
		t.Fatalf("ready agent did not unblock the existing task: %+v", finished)
	}
	var updated agents.Agent
	a.call("describe_agent", object{"agent_id": inventory.Agents[0].ID}, &updated)
	for _, action := range updated.Actions {
		if len(action.MissingRequirements) != 0 {
			t.Fatalf("rediscovery retained resolved requirements: %+v", action)
		}
	}
	var listed struct {
		Operations []operations.Operation `json:"operations"`
	}
	a.call("list_operations", object{}, &listed)
	if len(listed.Operations) != 1 || listed.Operations[0].Preflight == nil || !listed.Operations[0].Preflight.Ready {
		t.Fatalf("operation summary omitted preflight: %+v", listed)
	}
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": "Readiness restored; SEO checked", "findings": []object{}}}, nil)
}
