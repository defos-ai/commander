package e2e_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func TestE2ESessionReuseAfterClosingItsContinuation(t *testing.T) {
	var creations atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/create/") {
			creations.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(peer.Close)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Request("POST", "/api/v1/missions", map[string]any{
		"name": "retained-session", "objectives": []string{"Review a resource until the operator completes the operation"},
		"required_actions": []string{"connect-resource"},
		"policies":         map[string]any{"completion": map[string]any{"decision_by": "operator"}},
		"input_schema":     map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"},
	}, http.StatusCreated, nil)
	op := s.Start("retained-session")
	resource := createSession(t, s, op)
	scan := s.Continue(s.Task(op, resource.ID), "use-resource", map[string]any{})
	scan = s.Finished(op, scan.ID)
	s.Request("POST", "/api/v1/operations/"+op+"/tasks/"+scan.ID+"/continuation/resolve", map[string]any{
		"version": scan.Continuation.Version, "actions": []any{},
	}, http.StatusOK, nil)
	closed := s.Task(op, scan.ID)
	mcp := s.MCP()
	var receipt struct {
		Status   operations.Status `json:"status"`
		ReportID string            `json:"report_id"`
	}

	mcp.Call("complete_operation", map[string]any{"operation_id": op, "output": map[string]any{}}, &receipt)
	pending := s.Operation(op)

	if receipt.Status.Terminal() || receipt.ReportID != "" || pending.Status.Terminal() || string(pending.CompletionDraft) != `{}` {
		t.Fatalf("operator review completed the operation or lost its draft: %+v", pending)
	}
	var reused operations.Task
	s.Request("POST", "/api/v1/operations/"+op+"/tasks", map[string]any{
		"action_name": "use-resource", "session_task_id": closed.ID, "input": map[string]any{},
	}, http.StatusCreated, &reused)
	reused = s.Finished(op, reused.ID)
	assertIndependentSessionReuse(t, reused, resource)
	if !reflect.DeepEqual(s.Task(op, closed.ID).Continuation, closed.Continuation) {
		t.Fatal("independent reuse reopened or changed the resolved continuation")
	}

	s.Restart()
	s.Worker(2)
	restored := s.Task(op, reused.ID)
	if restored.SessionIntent == nil || *restored.SessionIntent != *reused.SessionIntent || restored.AgentID != reused.AgentID {
		t.Fatalf("controller restart changed the accepted session binding: %+v", restored)
	}
	var next operations.Task

	mcp.Call("assign_task", map[string]any{
		"operation_id": op, "action_name": "use-resource", "session_task_id": restored.ID, "input": map[string]any{},
	}, &next)
	next = s.Finished(op, next.ID)

	assertIndependentSessionReuse(t, next, resource)
	if !reflect.DeepEqual(s.Task(op, closed.ID).Continuation, closed.Continuation) || creations.Load() != 1 {
		t.Fatalf("reuse changed the closed branch or recreated the resource: creations=%d", creations.Load())
	}
	if pending := s.Operation(op); pending.Status.Terminal() || pending.ReportID != "" || string(pending.CompletionDraft) != `{}` {
		t.Fatalf("continued work approved or discarded the operator draft: %+v", pending)
	}

	stateless := s.Assign(op, "mark-owner", map[string]any{})
	s.Finished(op, stateless.ID)
	other := s.Start("retained-session")
	tasksBefore := len(s.Operation(op).Tasks)
	for _, scenario := range []struct {
		operation, action, source string
		status                    int
	}{
		{op, "use-resource", "not-a-uuid", http.StatusBadRequest},
		{op, "use-resource", id.New(), http.StatusNotFound},
		{other, "use-resource", closed.ID, http.StatusNotFound},
		{op, "use-resource", stateless.ID, http.StatusUnprocessableEntity},
		{op, "mark-owner", closed.ID, http.StatusUnprocessableEntity},
	} {
		s.Request("POST", "/api/v1/operations/"+scenario.operation+"/tasks", map[string]any{
			"action_name": scenario.action, "session_task_id": scenario.source, "input": map[string]any{},
		}, scenario.status, nil)
	}
	s.Request("POST", "/api/v1/operations/"+op+"/tasks", map[string]any{
		"action_name": "use-resource", "input": map[string]any{},
	}, http.StatusUnprocessableEntity, nil)

	var question operations.Question
	s.Request("POST", "/api/v1/operations/"+op+"/questions", map[string]any{
		"task_id": next.ID, "version": next.Continuation.Version, "text": "Continue using this resource?",
	}, http.StatusCreated, &question)

	s.Request("POST", "/api/v1/operations/"+op+"/tasks", map[string]any{
		"action_name": "use-resource", "session_task_id": next.ID, "input": map[string]any{},
	}, http.StatusConflict, nil)

	s.Request("POST", "/api/v1/operations/"+op+"/questions/"+question.ID+"/answer", map[string]any{"answer": "Continue"}, http.StatusOK, nil)
	s.Control(resource.Reference, "close")
	s.Request("POST", "/api/v1/operations/"+op+"/tasks", map[string]any{
		"action_name": "use-resource", "session_task_id": closed.ID, "input": map[string]any{},
	}, http.StatusUnprocessableEntity, nil)
	if len(s.Operation(op).Tasks) != tasksBefore || len(s.Operation(other).Tasks) != 0 || creations.Load() != 1 {
		t.Fatal("a rejected reuse queued work or recreated a resource")
	}

	s.Request("POST", "/api/v1/operations/"+op+"/complete", map[string]any{"output": map[string]any{}}, http.StatusOK, nil)
	completed := s.Operation(op)
	if completed.Status != operations.Completed || completed.CompletedBy != operations.CompletionOperator || len(completed.CompletionDraft) != 0 {
		t.Fatalf("operator could not complete continued work explicitly: %+v", completed)
	}
}

func assertIndependentSessionReuse(t *testing.T, task operations.Task, resource sessions.Session) {
	t.Helper()
	if task.SessionIntent == nil || task.SessionIntent.Mode != sessions.Use || task.SessionIntent.ScopeID != resource.ScopeID ||
		task.SessionIntent.ID != resource.ID || task.AgentID != resource.OwnerAgentID || task.ParentTaskID != "" || string(task.Input) != `{}` {
		t.Fatalf("independent assignment changed the resource, owner, branch or business input: %+v", task)
	}
	var output struct {
		Owner        string `json:"owner"`
		ID           string `json:"session_id"`
		CreationTask string `json:"creation_task"`
		Isolated     bool   `json:"checkpoint_isolated"`
	}
	if task.Result == nil {
		t.Fatal("session reuse did not retain its result")
	}
	if err := json.Unmarshal(task.Result.Output, &output); err != nil || output.Owner != resource.OwnerAgentID || output.ID != resource.ID ||
		output.CreationTask != resource.ID || !output.Isolated {
		t.Fatalf("action did not receive the retained creation binding: %+v, %v", output, err)
	}
}
