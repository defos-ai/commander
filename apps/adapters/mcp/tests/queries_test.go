package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type eventPage struct {
	Events       []json.RawMessage `json:"events"`
	NextSequence uint64            `json:"next_sequence"`
}

func TestMCPQueriesStartWithoutController(t *testing.T) {
	// Arrange: only the mission catalog exists; no agent has reported actions.
	d := newDeployment(t)
	seedMission(t, d)

	// Act: start MCP independently. Its health check and queries must succeed.
	endpoint, _ := d.mcpOnly(t, true)
	session := connect(t, d.ctx, endpoint)
	ctx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
	defer cancel()
	missions := decode[struct{ Missions []struct{ Name string } }](t, call(t, ctx, session, "list_missions", map[string]any{}))
	actions := decode[struct{ Actions []struct{ Name string } }](t, call(t, ctx, session, "list_actions", map[string]any{}))
	operations := decode[struct{ Operations []operationView }](t, call(t, ctx, session, "list_operations", map[string]any{}))
	registered := decode[struct{ Agents []agentView }](t, call(t, ctx, session, "list_agents", map[string]any{}))

	// Assert: reads preserve the catalogs and return no coordinated work.
	if len(missions.Missions) != 1 || missions.Missions[0].Name != "local-observation" || len(actions.Actions) != 0 {
		t.Fatalf("catalogs unavailable without controller: missions=%+v actions=%+v", missions, actions)
	}
	if len(operations.Operations) != 0 || len(registered.Agents) != 0 || d.controllerProcess != nil {
		t.Fatalf("unexpected coordination state: operations=%+v agents=%+v", operations, registered)
	}
	data, err := os.ReadFile(filepath.Join(d.root, "database", "missions.json"))
	if err != nil || string(data) != missionCatalog {
		t.Fatalf("queries changed the mission catalog: %s, %v", data, err)
	}
}

func TestMCPOfflineQueriesExpireAgentAndWaitForEvents(t *testing.T) {
	// Arrange: a real agent has an active recorded lease and an unfinished
	// operation has a durable event cursor when the controller crashes.
	d := newDeployment(t)
	seedMission(t, d)
	d.controller(t, "--heartbeat-timeout=600ms")
	endpoint, _ := d.mcp(t, false)
	agentProcess := d.agent(t)
	session := connect(t, d.ctx, endpoint)
	agent := readyAgents(t, d, session, 1)[0]
	operation := startMission(t, d, session)
	before := decode[eventPage](t, call(t, d.ctx, session, "operation_events", map[string]any{"operation_id": operation.ID}))
	d.controllerProcess.kill(t)

	// Act: long polling reads the persisted cursor while coordination is down.
	started := time.Now()
	ctx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
	defer cancel()
	after := decode[eventPage](t, call(t, ctx, session, "operation_events", map[string]any{
		"operation_id": operation.ID, "after_sequence": before.NextSequence, "wait_seconds": 1,
	}))
	waited := time.Since(started)
	current := decode[agentView](t, call(t, ctx, session, "describe_agent", map[string]any{"agent_id": agent.ID}))
	registered := decode[struct{ Agents []agentView }](t, call(t, ctx, session, "list_agents", map[string]any{}))
	rejected(t, ctx, session, "operation_events", map[string]any{"operation_id": operation.ID, "after_sequence": before.NextSequence + 1})

	// Assert: timeout returns an unchanged cursor, and a stale stored heartbeat
	// does not claim the agent is available just because its process is alive.
	if len(after.Events) != 0 || after.NextSequence != before.NextSequence || waited < 900*time.Millisecond {
		t.Fatalf("offline event wait: page=%+v waited=%v", after, waited)
	}
	if current.ID != agent.ID || current.Status != "unavailable" || !current.Stale || len(registered.Agents) != 1 || !registered.Agents[0].Stale || agentProcess.stopped() {
		t.Fatalf("offline inventory did not expire: agent=%+v list=%+v", current, registered)
	}
}
