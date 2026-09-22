package cli_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Exercise the public JSON contract without importing controller internals.
type continuationOperation struct {
	ID    string
	Tasks []struct {
		ID, Status   string
		Continuation *struct {
			Status          string
			ProposedActions []any    `json:"proposed_actions"`
			CreatedTaskIDs  []string `json:"created_task_ids"`
		}
	}
}

func TestMCPExposesPendingContinuationsAfterRestart(t *testing.T) {
	// Arrange: a real agent executes one ordinary action through MCP.
	d := newDeployment(t)
	seedMission(t, d)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "observation")
	}))
	defer fixture.Close()
	endpoint, adapter := d.mcp(t, false)
	d.agent(t)
	session := connect(t, d.ctx, endpoint)
	agent := readyAgents(t, d, session, 1)[0]

	operation := decode[continuationOperation](t, call(t, d.ctx, session, "start_operation", map[string]any{
		"mission_name": "local-observation", "input": map[string]any{},
	}))
	task := assignLinkCheck(t, d, session, operation.ID, agent.ID, fixture.URL)
	waitTask(t, d, session, operation.ID, task.ID, "succeeded")
	before := decode[continuationOperation](t, call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": operation.ID}))

	// Act: replace both coordination and adapter processes, then query the same ID.
	adapter.interrupt(t)
	d.controllerProcess.interrupt(t)
	d.controller(t)
	endpoint, _ = d.mcpOnly(t, false)
	session = connect(t, d.ctx, endpoint)
	after := decode[continuationOperation](t, call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": operation.ID}))

	// Assert: no proposal is still a pending decision, with no follow-up tasks.
	if len(after.Tasks) != 1 || !reflect.DeepEqual(before, after) {
		t.Fatalf("continuation changed across restart: before=%+v after=%+v", before, after)
	}
	continuation := after.Tasks[0].Continuation
	if continuation == nil || continuation.Status != "pending" || continuation.ProposedActions == nil || len(continuation.ProposedActions) != 0 || continuation.CreatedTaskIDs == nil || len(continuation.CreatedTaskIDs) != 0 {
		t.Fatalf("expected an empty pending continuation: %+v", continuation)
	}
	// Explicit operation completion closes remaining model decisions.
	completed := decode[operationView](t, call(t, d.ctx, session, "complete_operation", map[string]any{
		"operation_id": operation.ID, "output": map[string]any{"summary": "reviewed"},
	}))
	if completed.Status != "completed" {
		t.Fatalf("capture blocked explicit completion: %+v", completed)
	}
}
