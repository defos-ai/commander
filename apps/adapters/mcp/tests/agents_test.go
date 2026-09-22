package mcp_test

import (
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"testing"
	"time"
)

func TestAgentsAndActions(t *testing.T) {
	// Arrange: wait for real Compose agents to announce all installed actions.
	a := connect(t)
	var list struct {
		Agents []agents.Agent `json:"agents"`
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		a.call("list_agents", object{}, &list)
		if len(list.Agents) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected two agents: %+v", list)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Act: inspect the public catalogs and refresh one running agent.
	var catalog struct {
		Actions []actions.Action `json:"actions"`
	}
	a.call("list_actions", object{}, &catalog)
	if len(catalog.Actions) != 6 {
		t.Fatalf("actions=%+v", catalog)
	}
	for _, agent := range list.Agents {
		if agent.Stale || len(agent.Actions) != 6 {
			t.Fatalf("incomplete/stale agent: %+v", agent)
		}
		for _, action := range agent.Actions {
			if action.Status != actions.Ready {
				t.Fatalf("Compose must prepare dependencies: %s: %+v", agent.ID, action)
			}
		}
	}
	var detail agents.Agent
	a.call("describe_agent", object{"agent_id": list.Agents[0].ID}, &detail)
	a.call("discover_actions", object{"agent_id": detail.ID}, &detail)
	var action actions.Action
	a.call("describe_action", object{"agent_id": detail.ID, "action_name": "check-seo"}, &action)
	// Assert: schemas can drive calls, failures leave the session usable.
	if len(action.InputSchema) == 0 || len(action.OutputSchema) == 0 {
		t.Fatalf("missing schemas: %+v", action)
	}
	a.fails("run_action", object{"agent_id": detail.ID, "action_name": "check-seo", "input": object{"url": "file:///tmp"}}, "http")
	var result actions.ActionOutput
	a.call("run_action", object{"agent_id": detail.ID, "action_name": "check-seo", "input": object{"url": website(t)}}, &result)
	if len(result.Output) == 0 && result.OutputArtifact == nil {
		t.Fatal("action returned no result")
	}
}
