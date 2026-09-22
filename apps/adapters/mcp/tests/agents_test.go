package mcp_test

import (
	"context"
	"testing"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/operations"
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

func TestQueuedActionResumesAfterDependencyRediscovery(t *testing.T) {
	// Arrange: restrict one agent's discovery to an empty executable search path.
	// This exercises the same unavailable state as a missing browser installation.
	a := connect(t)
	var list struct {
		Agents []agents.Agent `json:"agents"`
	}
	a.call("list_agents", object{}, &list)
	agentID := list.Agents[0].ID
	var unavailable agents.Agent
	a.call("discover_actions", object{"agent_id": agentID, "scope": object{"paths": []string{}}}, &unavailable)
	// Restore the shared agent even if an assertion below fails.
	t.Cleanup(func() {
		// testing cancels t.Context before cleanup; restoration needs its own
		// bounded lifetime while the MCP session is still open.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), 5*time.Second)
		defer cancel()
		cleanup := *a
		cleanup.ctx = ctx
		cleanup.call("discover_actions", object{"agent_id": agentID}, nil)
	})
	assertActionStatus(t, unavailable, "capture-page", actions.Unavailable)
	url := website(t)
	op := a.start(url)
	var queued operations.Task
	a.call("assign_task", object{"operation_id": op.ID, "agent_id": agentID, "action_name": "capture-page", "input": object{"url": url}}, &queued)
	// A successful task on the other agent proves the scheduler has progressed
	// while the unavailable task remains queued, rather than checking immediately.
	a.call("assign_task", object{"operation_id": op.ID, "agent_id": list.Agents[1].ID, "action_name": "check-seo", "input": object{"url": url}}, nil)
	blocked := a.wait(op.ID, func(op operationDetail) bool { return op.Tasks[1].Status == operations.TaskSucceeded })
	if blocked.Tasks[0].Status != operations.TaskQueued || blocked.Tasks[0].Result != nil {
		t.Fatalf("unavailable action was dispatched: %+v", blocked.Tasks[0])
	}

	// Act: normal discovery finds the Compose browser again; no task resubmission.
	var ready agents.Agent
	a.call("discover_actions", object{"agent_id": agentID}, &ready)
	assertActionStatus(t, ready, "capture-page", actions.Ready)
	settled := a.wait(op.ID, allTasksSucceeded)

	// Assert: the original task runs once, preserving identity and its artifacts.
	resumed := settled.Tasks[0]
	if len(settled.Tasks) != 2 || resumed.ID != queued.ID || resumed.AgentID != agentID || len(resumed.Result.Artifacts) != 2 {
		t.Fatalf("rediscovery duplicated or replaced work: %+v", settled.Tasks)
	}
	var events struct {
		Events []operations.Event `json:"events"`
	}
	a.call("operation_events", object{"operation_id": op.ID}, &events)
	started := 0
	for _, event := range events.Events {
		if event.TaskID == queued.ID && event.Type == operations.TaskDispatched {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("task was dispatched %d times", started)
	}
	for _, ref := range resumed.Result.Artifacts {
		assertPNG(t, a, ref.ID)
	}
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": "Dependency restored and queued work completed", "findings": []object{}}}, nil)
}

func assertActionStatus(t *testing.T, agent agents.Agent, name string, want actions.Status) {
	t.Helper()
	for _, action := range agent.Actions {
		if action.Name == name {
			if action.Status != want || (want == actions.Unavailable && action.StatusReason == "") {
				t.Fatalf("unexpected action readiness: %+v", action)
			}
			return
		}
	}
	t.Fatalf("agent did not advertise %s", name)
}
