package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/operations"
)

// The shared API/MCP scenarios need a known action with no executor so queued
// work can be inspected and cancelled. Load their actual mission fixture here.
func TestE2ESharedFixtureQueuesUnavailableAction(t *testing.T) {
	root, err := filepath.Abs("../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "apps/agents/spawner/tests/e2e/missions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var missions []map[string]any
	if err := json.Unmarshal(raw, &missions); err != nil {
		t.Fatal(err)
	}
	s := e2efixtures.NewSessionStack(t, "")
	for _, mission := range missions {
		if mission["name"] == "analyze-text" {
			s.Request("POST", "/api/v1/missions", mission, 201, nil)
		}
	}
	var op operations.Operation
	s.Request("POST", "/api/v1/operations", map[string]any{"mission_name": "analyze-text", "input": map[string]string{"text": "Queued fixture work"}}, 201, &op)
	task := s.Assign(op.ID, "unavailable-test-action", map[string]any{})
	if task.Status != operations.TaskQueued || task.AgentID != "" {
		t.Fatalf("unavailable fixture work did not stay queued: %+v", task)
	}
	path := "/api/v1/operations/" + op.ID
	var graph models.OperationGraphResponse
	s.Request("GET", path+"/graph", nil, 200, &graph)
	if len(graph.Nodes) != 1 || graph.Nodes[0].Task == nil || graph.Nodes[0].Task.ID != task.ID || graph.Nodes[0].Task.Status != "queued" {
		t.Fatalf("graph lost queued fixture work: %+v", graph)
	}
	s.Request("POST", path+"/cancel", map[string]any{}, 200, nil)
	s.Request("GET", path+"/graph", nil, 200, &graph)
	if graph.Status != "cancelled" || len(graph.Nodes) != 1 || graph.Nodes[0].Task == nil || graph.Nodes[0].Task.Status != "cancelled" {
		t.Fatalf("graph lost cancelled fixture work: %+v", graph)
	}
}
