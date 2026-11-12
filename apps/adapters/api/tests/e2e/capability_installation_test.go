package e2e_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestE2ECapabilityInstallationResumesWaitingOperation(t *testing.T) {
	installationRequested := make(chan struct{}, 1)
	installation, finishInstallation := context.WithCancel(t.Context())
	defer finishInstallation()
	var creations atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/create/") {
			creations.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/create/fixture-agent-00000001/") {
			select {
			case installationRequested <- struct{}{}:
			default:
			}
			select {
			case <-installation.Done():
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(peer.Close)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Request("POST", "/api/v1/missions", map[string]any{
		"name": "requires-secondary", "objectives": []string{"Use the capability installed by separate work"},
		"required_actions": []string{"connect-secondary"},
		"action_definitions": []any{map[string]any{
			"name": "connect-secondary", "session_mode": "open",
			"input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"},
		}},
		"input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"},
	}, http.StatusCreated, nil)
	s.Mission("install-agent", "connect-resource")
	mcp := s.MCP()
	var primary operations.Operation
	mcp.Call("start_operation", map[string]any{"mission_name": "requires-secondary", "input": map[string]any{}}, &primary)
	var queued operations.Task
	mcp.Call("assign_task", map[string]any{"operation_id": primary.ID, "action_name": "connect-secondary", "input": map[string]any{}}, &queued)
	before := s.Operation(primary.ID)
	if before.Status != operations.Waiting || before.Preflight == nil || before.Preflight.Ready ||
		len(before.Tasks) != 1 || before.Tasks[0].ID != queued.ID || before.Tasks[0].Status != operations.TaskQueued || before.Tasks[0].AgentID != "" {
		t.Fatalf("missing capability did not retain the original queued work: %+v", before)
	}
	var bootstrap operations.Operation

	mcp.Call("start_operation", map[string]any{"mission_name": "install-agent", "input": map[string]any{}}, &bootstrap)
	var installer operations.Task
	mcp.Call("assign_task", map[string]any{"operation_id": bootstrap.ID, "action_name": "connect-resource", "input": map[string]any{}}, &installer)
	select {
	case <-installationRequested:
	case <-time.After(5 * time.Second):
		t.Fatal("separate installation work did not dispatch while the primary operation waited")
	}

	blocked := s.Task(primary.ID, queued.ID)
	if bootstrap.ID == primary.ID || blocked.Status != operations.TaskQueued || blocked.AgentID != "" || creations.Load() != 1 {
		t.Fatalf("primary work ran before its capability arrived: primary=%+v creations=%d", blocked, creations.Load())
	}
	finishInstallation()
	installed := s.Finished(bootstrap.ID, installer.ID)
	mcp.Call("complete_operation", map[string]any{"operation_id": bootstrap.ID, "output": map[string]any{}}, &operations.Operation{})
	waiting := s.Operation(primary.ID)
	if waiting.Status != operations.Waiting || waiting.Preflight == nil || waiting.Preflight.Ready ||
		len(waiting.Tasks) != 1 || waiting.Tasks[0].Status != operations.TaskQueued || waiting.Tasks[0].AgentID != "" || creations.Load() != 1 {
		t.Fatalf("installation completion was mistaken for agent readiness: %+v", waiting)
	}

	s.Worker(2, "FIXTURE_POLICY_ACTIONS=true")
	resumed := s.Finished(primary.ID, queued.ID)
	after := s.Operation(primary.ID)

	if installed.AgentID != "fixture-agent-00000001" || resumed.AgentID != "fixture-agent-00000002" ||
		resumed.OperationID != primary.ID || resumed.ID != queued.ID || resumed.CommandID != queued.CommandID ||
		len(after.Tasks) != 1 || after.Preflight == nil || !after.Preflight.Ready || after.Status.Terminal() || creations.Load() != 2 {
		t.Fatalf("registration did not resume the same primary work: installed=%+v resumed=%+v operation=%+v creations=%d", installed, resumed, after, creations.Load())
	}
	mcp.Call("complete_operation", map[string]any{"operation_id": primary.ID, "output": map[string]any{}}, &operations.Operation{})
}
