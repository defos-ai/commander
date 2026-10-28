package mcp_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestAffinityWaitsForItsAgentWithoutBlockingAnotherOperation(t *testing.T) {
	a := fixtures.Connect(t)
	for range 2 {
		installAffinityFixtureAgent(t, a)
	}
	resumeURL, release := fixtures.AffinityGate(t)
	start := func(message string) operations.Operation {
		t.Helper()
		var op operations.Operation
		a.Call("start_operation", fixtures.Object{"mission_name": "fixture-affinity", "input": fixtures.Object{"message": message}}, &op)
		if op.Preflight == nil || !op.Preflight.Ready {
			t.Fatalf("fixture mission is not ready: %+v", op.Preflight)
		}
		return op
	}
	assign := func(operationID string, input fixtures.Object) operations.Task {
		t.Helper()
		var task operations.Task
		a.Call("assign_task", fixtures.Object{"operation_id": operationID, "action_name": "fixture-echo", "input": input}, &task)
		return task
	}

	operation := start("owner")
	assertAffinityProjection(t, a.Get(operation.ID).Affinity, "")
	first := assign(operation.ID, fixtures.Object{"message": "owner", "resume_url": resumeURL})
	executed := waitSpawnerTask(t, a, operation.ID, first.ID)
	assertAffinityEcho(t, executed, "owner", executed.AgentID)
	owner := executed.AgentID
	blocked := a.Wait(operation.ID, func(op fixtures.OperationDetail) bool {
		return op.Preflight != nil && !op.Preflight.Ready
	})
	assertAffinityProjection(t, blocked.Affinity, owner)
	if len(blocked.Preflight.Actions) != 1 {
		t.Fatalf("unexpected affinity preflight: %+v", blocked.Preflight)
	}
	coverage := blocked.Preflight.Actions[0]
	if coverage.ActionName != "fixture-echo" || len(coverage.ReadyAgentIDs) != 0 || len(coverage.UnavailableAgents) != 1 || coverage.UnavailableAgents[0].AgentID != owner || coverage.UnavailableAgents[0].Reason == "" {
		t.Fatalf("preflight did not explain the unavailable affinity owner: %+v", coverage)
	}
	queued := assign(operation.ID, fixtures.Object{"message": "same owner"})
	if queued.Status != operations.TaskQueued || queued.AgentID != "" {
		t.Fatalf("blocked task must remain unassigned: %+v", queued)
	}

	// Waiting is scoped to this operation. Another one can bind to a ready
	// instance while the first owner's action is unavailable.
	independent := start("independent")
	other := assign(independent.ID, fixtures.Object{"message": "independent"})
	other = waitSpawnerTask(t, a, independent.ID, other.ID)
	if other.AgentID == owner {
		t.Fatalf("independent operation used the unavailable agent: %+v", other)
	}
	assertAffinityEcho(t, other, "independent", other.AgentID)
	assertAffinityProjection(t, a.Get(independent.ID).Affinity, other.AgentID)
	a.Call("complete_operation", fixtures.Object{"operation_id": independent.ID, "output": fixtures.Object{"agent_id": other.AgentID}}, nil)

	waiting := a.Get(operation.ID)
	assertAffinityProjection(t, waiting.Affinity, owner)
	if len(waiting.Tasks) != 2 || waiting.Tasks[1].ID != queued.ID || waiting.Tasks[1].Status != operations.TaskQueued || waiting.Tasks[1].AgentID != "" || waiting.Preflight == nil || waiting.Preflight.Ready {
		t.Fatalf("queued work fell back while its owner was unavailable: %+v", waiting)
	}
	var listed struct {
		Operations []fixtures.OperationDetail `json:"operations"`
	}
	a.Call("list_operations", fixtures.Object{}, &listed)
	found := false
	for _, summary := range listed.Operations {
		if summary.ID == operation.ID {
			found = true
			assertAffinityProjection(t, summary.Affinity, owner)
		}
	}
	if !found {
		t.Fatal("operation list omitted the waiting operation")
	}

	release()
	resumed := waitSpawnerTask(t, a, operation.ID, queued.ID)
	assertAffinityEcho(t, resumed, "same owner", owner)
	waitFixtureAgent(t, a, owner)
	a.Call("complete_operation", fixtures.Object{"operation_id": operation.ID, "output": fixtures.Object{"agent_id": owner}}, nil)
	completed := a.Get(operation.ID)
	assertAffinityProjection(t, completed.Affinity, owner)
	if completed.Status != operations.Completed || len(completed.Tasks) != 2 {
		t.Fatalf("affinity operation did not complete with exactly two tasks: %+v", completed)
	}
	dispatches := map[string]int{}
	for _, event := range completed.Events {
		if event.Type == operations.TaskDispatched {
			dispatches[event.TaskID]++
		}
	}
	if len(dispatches) != 2 || dispatches[first.ID] != 1 || dispatches[queued.ID] != 1 {
		t.Fatalf("waiting or recovery duplicated dispatch: %+v", dispatches)
	}
}

func installAffinityFixtureAgent(t *testing.T, a *fixtures.Client) {
	t.Helper()
	input := fixtures.Object{"package": "github.com/defos-ai/fixture-agent@v0.1.0"}
	var operation operations.Operation
	a.Call("start_operation", fixtures.Object{"mission_name": "install-agent", "input": input}, &operation)
	var task operations.Task
	a.Call("assign_task", fixtures.Object{"operation_id": operation.ID, "action_name": "spawn-agent", "input": input}, &task)
	task = waitSpawnerTask(t, a, operation.ID, task.ID)
	var spawned struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(task.Result.Output, &spawned); err != nil {
		t.Fatal(err)
	}
	waitFixtureAgent(t, a, spawned.AgentID)
	decision := fixtures.ModelDecision(a.Wait(operation.ID, func(op fixtures.OperationDetail) bool { return fixtures.ModelDecision(op) != nil }))
	a.Resolve(operation.ID, *decision, []fixtures.Object{})
	a.Call("complete_operation", fixtures.Object{"operation_id": operation.ID, "output": fixtures.Object{"agent_id": spawned.AgentID}}, nil)
}

func assertAffinityProjection(t *testing.T, affinity *operations.Affinity, owner string) {
	t.Helper()
	if affinity == nil || !slices.Equal(affinity.ForActions, []string{"fixture-echo"}) || affinity.AgentID != owner || affinity.Error != "" {
		t.Fatalf("affinity projection does not preserve the group and owner %q: %+v", owner, affinity)
	}
}

func assertAffinityEcho(t *testing.T, task operations.Task, message, owner string) {
	t.Helper()
	var output struct {
		Message string `json:"message"`
		AgentID string `json:"agent_id"`
	}
	if task.Result == nil {
		t.Fatalf("task did not save its result: %+v", task)
	}
	if err := json.Unmarshal(task.Result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if task.AgentID != owner || output.AgentID != owner || output.Message != message {
		t.Fatalf("echo result differs from expected owner %q and message %q: task=%+v output=%+v", owner, message, task, output)
	}
}
