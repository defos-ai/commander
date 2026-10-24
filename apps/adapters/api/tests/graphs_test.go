package api_test

import (
	"net/http"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

func TestGraphReflectsRealTaskBranchesAndArtifactFiles(t *testing.T) {
	a := connect(t)
	op := a.start(fixtureText)
	base := operationPath(op.ID)
	empty := a.request(http.MethodGet, base+"/graph", nil)
	initial := decode[models.OperationGraphResponse](t, empty, http.StatusOK)
	if initial.OperationID != op.ID || initial.Nodes == nil || initial.Edges == nil || len(initial.Nodes) != 0 || len(initial.Edges) != 0 {
		t.Fatalf("new graph should contain empty arrays: %+v", initial)
	}
	assertSchema(t, a, "OperationGraph", empty)
	root := a.task(op.ID, "analyze-text", object{"text": fixtureText})
	finished := a.wait(op.ID, func(op models.OperationResponse) bool { return tasksSettled(op) && modelDecision(op) != nil })
	root = finished.Tasks[0]
	if root.Status != "succeeded" {
		t.Fatalf("analysis task failed: %+v", root)
	}
	if len(finished.Tasks) != 2 || finished.Tasks[1].Status != "succeeded" {
		t.Fatalf("proposed summary branch failed: %+v", finished.Tasks)
	}
	queued := a.task(op.ID, "unavailable-test-action", object{})
	// Queue acceptance precedes the scheduler's persisted optional-action assessment.
	snapshot := a.wait(op.ID, func(op models.OperationResponse) bool {
		if op.Preflight == nil || op.Preflight.Ready {
			return false
		}
		for _, action := range op.Preflight.Actions {
			if action.ActionName == queued.ActionName {
				return len(action.ReadyAgentIDs) == 0
			}
		}
		return false
	})
	result := a.request(http.MethodGet, base+"/graph", nil)
	graph := decode[models.OperationGraphResponse](t, result, http.StatusOK)
	assertSchema(t, a, "OperationGraph", result)
	if graph.OperationID != op.ID || graph.Status != snapshot.Status || !graph.UpdatedAt.Equal(snapshot.UpdatedAt) || len(graph.Nodes) != 5 || len(graph.Edges) != 3 {
		t.Fatalf("graph lost task/file snapshot: %+v", graph)
	}
	nodes := map[string]models.GraphNodeResponse{}
	for _, node := range graph.Nodes {
		if _, exists := nodes[node.ID]; exists {
			t.Fatalf("duplicate graph node: %s", node.ID)
		}
		nodes[node.ID] = node
		payloads := 0
		if node.Task != nil {
			payloads++
		}
		if node.Artifact != nil {
			payloads++
		}
		if node.File != nil {
			payloads++
		}
		if payloads != 1 {
			t.Fatalf("node must contain exactly one payload: %+v", node)
		}
	}
	for _, task := range snapshot.Tasks {
		node := nodes["task:"+task.ID]
		if node.Kind != "task" || node.Task == nil || node.Task.Status != task.Status || node.Task.ActionName != task.ActionName || node.Task.ParentTaskID != task.ParentTaskID {
			t.Fatalf("graph task differs: %+v", node)
		}
	}
	if node := nodes["task:"+queued.ID]; node.Task.StartedAt != nil || node.Task.FinishedAt != nil || node.Task.AgentID != "" {
		t.Fatalf("queued task invented execution: %+v", node)
	}
	artifact := finished.Tasks[1].Result.Artifacts[0]
	if node := nodes["artifact:"+artifact.ID]; node.Artifact == nil || node.Artifact.CommandID != finished.Tasks[1].CommandID {
		t.Fatalf("artifact node lost producer identity: %+v", node)
	}
	wantEdges := map[models.GraphEdgeResponse]bool{
		{Source: "task:" + root.ID, Target: "task:" + finished.Tasks[1].ID, Kind: "parent_child"}:     true,
		{Source: "task:" + finished.Tasks[1].ID, Target: "artifact:" + artifact.ID, Kind: "produced"}: true,
	}
	for _, file := range artifact.Files {
		id := "file:" + artifact.ID + ":" + file.ID
		if node := nodes[id]; node.File == nil || node.File.ArtifactID != artifact.ID || node.File.SHA256 != file.SHA256 || node.File.Size != file.Size {
			t.Fatalf("file node differs from descriptor: %+v", node)
		}
		wantEdges[models.GraphEdgeResponse{Source: "artifact:" + artifact.ID, Target: id, Kind: "contains"}] = true
	}
	for _, edge := range graph.Edges {
		if !wantEdges[edge] || nodes[edge.Source].ID == "" || nodes[edge.Target].ID == "" {
			t.Fatalf("duplicate, dangling or unexpected edge: %+v", edge)
		}
		delete(wantEdges, edge)
	}
	if len(wantEdges) != 0 {
		t.Fatalf("missing edges: %+v", wantEdges)
	}
	if repeated := a.request(http.MethodGet, base+"/graph", nil); repeated.body != result.body {
		t.Fatal("unchanged snapshot changed node IDs/order")
	}
	if after := read[models.OperationResponse](a, base); !after.UpdatedAt.Equal(snapshot.UpdatedAt) {
		t.Fatal("graph read mutated operation state")
	}
	post[models.OperationResponse](a, base+"/cancel", nil, http.StatusOK)
	fresh := read[models.OperationGraphResponse](a, base+"/graph")
	if fresh.Status != "cancelled" || fresh.UpdatedAt.Equal(graph.UpdatedAt) || len(fresh.Nodes) != len(graph.Nodes) {
		t.Fatalf("graph did not observe cancellation: %+v", fresh)
	}
	for _, node := range fresh.Nodes {
		if node.ID == "task:"+queued.ID && (node.Task.Status != "cancelled" || node.Task.FinishedAt == nil) {
			t.Fatalf("cancelled queued task was not refreshed: %+v", node)
		}
	}
}
