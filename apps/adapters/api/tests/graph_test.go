package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/adapters/api/models"
	"commander/framework/domain/actions"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/operations"
)

func TestOperationGraphReturnsSavedBranchesAndArtifactDescriptors(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	op := operationRecord(1)
	for i, status := range []operations.TaskStatus{operations.TaskQueued, operations.TaskRunning, operations.TaskSucceeded, operations.TaskFailed, operations.TaskCancelled, operations.TaskInterrupted} {
		task := taskRecord(op.ID, 10+i)
		task.Status, task.ParentTaskID = status, ""
		if status == operations.TaskQueued || status == operations.TaskRunning {
			task.Result, task.Continuation, task.FinishedAt = nil, nil, nil
		}
		if status == operations.TaskQueued {
			task.AgentID, task.StepID, task.CommandID, task.StartedAt = "", "", "", time.Time{}
		}
		if status == operations.TaskFailed {
			task.Error = "Review failed"
		}
		op.Tasks = append(op.Tasks, task)
	}
	// Two branches and independent roots; missing and self parents add no edges.
	op.Tasks[1].ParentTaskID, op.Tasks[2].ParentTaskID = op.Tasks[0].ID, op.Tasks[0].ID
	op.Tasks[3].ParentTaskID = op.Tasks[1].ID
	op.Tasks[4].ParentTaskID, op.Tasks[5].ParentTaskID = recordID(999), op.Tasks[5].ID
	first := artifacts.Artifact{ID: recordID(100), Name: "Saved evidence", AgentID: op.Tasks[2].AgentID,
		ActionName: "review", CommandID: op.Tasks[2].CommandID, CreatedAt: op.CreatedAt,
		Files: []artifacts.ArtifactFile{
			{ID: recordID(200), Name: "review.txt", MediaType: "text/plain", Size: 12, SHA256: strings.Repeat("a", 64)},
			{ID: recordID(201), Name: "empty.bin", MediaType: "application/octet-stream", Size: 0, SHA256: strings.Repeat("b", 64)},
		}}
	second := first.Clone()
	second.ID, second.Name, second.CommandID, second.Files = recordID(101), "Other evidence", "", second.Files[:1]
	// Repeated references must retain each producer but share artifact/file nodes.
	// File identity is scoped to its artifact, even when file UUIDs coincide.
	op.Tasks[2].Result.Artifacts = []artifacts.Artifact{first, first}
	op.Tasks[3].Result.Artifacts = []artifacts.Artifact{first, second}
	op = saveOperation(t, store, op)
	// Neither artifact metadata nor blobs exist outside the operation snapshot.
	server := startAPI(t, append(fixture.localArgs(), "--swagger"))
	path := "/api/v1/operations/" + op.ID + "/graph"
	before, artifactsBefore := operationBytes(t, fixture, op.ID), artifactStorageFiles(t, fixture)
	result := server.request(t, http.MethodGet, path, authenticate)
	graph := decodeResponse[models.OperationGraphResponse](t, result)
	assertResponseSchema(t, server, "OperationGraph", result)
	assertSecurityHeaders(t, result, false)
	if graph.OperationID != op.ID || graph.Status != string(op.Status) || !graph.UpdatedAt.Equal(op.UpdatedAt) {
		t.Fatalf("graph lost snapshot identity/status/time: %+v", graph)
	}
	if len(graph.Nodes) != 11 || len(graph.Edges) != 9 {
		t.Fatalf("graph duplicated nodes/edges or included proposed actions: nodes=%d edges=%d", len(graph.Nodes), len(graph.Edges))
	}
	nodes := make(map[string]models.GraphNodeResponse, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if _, exists := nodes[node.ID]; exists {
			t.Fatalf("duplicate graph node %q", node.ID)
		}
		nodes[node.ID] = node
	}
	for _, task := range op.Tasks {
		node := nodes["task:"+task.ID]
		want := models.GraphTaskResponse{ID: task.ID, ParentTaskID: task.ParentTaskID, StepID: task.StepID, ActionName: task.ActionName,
			AgentID: task.AgentID, CommandID: task.CommandID, Status: string(task.Status), Error: task.Error, FinishedAt: task.FinishedAt}
		if !task.StartedAt.IsZero() {
			want.StartedAt = &task.StartedAt
		}
		if node.Kind != "task" || node.Task == nil || !reflect.DeepEqual(*node.Task, want) {
			t.Fatalf("task projection differs: got %+v, want %+v", node, want)
		}
	}
	for _, artifact := range []artifacts.Artifact{first, second} {
		node := nodes["artifact:"+artifact.ID]
		want := models.GraphArtifactResponse{ID: artifact.ID, Name: artifact.Name, AgentID: artifact.AgentID,
			ActionName: artifact.ActionName, CommandID: artifact.CommandID, CreatedAt: artifact.CreatedAt}
		if node.Kind != "artifact" || node.Artifact == nil || !reflect.DeepEqual(*node.Artifact, want) {
			t.Fatalf("artifact projection differs: got %+v, want %+v", node, want)
		}
		for _, file := range artifact.Files {
			node := nodes["file:"+artifact.ID+":"+file.ID]
			want := models.GraphFileResponse{ArtifactID: artifact.ID, ID: file.ID, Name: file.Name, MediaType: file.MediaType, Size: file.Size, SHA256: file.SHA256}
			if node.Kind != "file" || node.File == nil || !reflect.DeepEqual(*node.File, want) {
				t.Fatalf("file projection differs: got %+v, want %+v", node, want)
			}
		}
	}
	wantEdges := map[[3]string]bool{
		{"parent_child", "task:" + op.Tasks[0].ID, "task:" + op.Tasks[1].ID}:                  true,
		{"parent_child", "task:" + op.Tasks[0].ID, "task:" + op.Tasks[2].ID}:                  true,
		{"parent_child", "task:" + op.Tasks[1].ID, "task:" + op.Tasks[3].ID}:                  true,
		{"produced", "task:" + op.Tasks[2].ID, "artifact:" + first.ID}:                        true,
		{"produced", "task:" + op.Tasks[3].ID, "artifact:" + first.ID}:                        true,
		{"produced", "task:" + op.Tasks[3].ID, "artifact:" + second.ID}:                       true,
		{"contains", "artifact:" + first.ID, "file:" + first.ID + ":" + first.Files[0].ID}:    true,
		{"contains", "artifact:" + first.ID, "file:" + first.ID + ":" + first.Files[1].ID}:    true,
		{"contains", "artifact:" + second.ID, "file:" + second.ID + ":" + second.Files[0].ID}: true,
	}
	for _, edge := range graph.Edges {
		key := [3]string{edge.Kind, edge.Source, edge.Target}
		if !wantEdges[key] || nodes[edge.Source].ID == "" || nodes[edge.Target].ID == "" {
			t.Fatalf("unexpected, duplicate or dangling edge: %+v", edge)
		}
		delete(wantEdges, key)
	}
	if len(wantEdges) != 0 {
		t.Fatalf("missing graph edges: %+v", wantEdges)
	}
	// Check wire omissions as well as decoded values: null/zero placeholders and
	// full task results must not escape into these deliberately compact nodes.
	wire := decodeResponse[struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
	}](t, result)
	for _, node := range wire.Nodes {
		var kind, nodeID string
		if err := json.Unmarshal(node["kind"], &kind); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(node["id"], &nodeID); err != nil {
			t.Fatal(err)
		}
		if len(node) != 3 || node[kind] == nil || string(node[kind]) == "null" {
			t.Fatalf("node requires exactly its matching data property: %+v", node)
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(node[kind], &data); err != nil {
			t.Fatal(err)
		}
		for _, omitted := range []string{"input", "output", "result", "continuation", "files", "next_actions"} {
			if _, exists := data[omitted]; exists {
				t.Fatalf("graph node exposed full record field %q", omitted)
			}
		}
		if nodeID == "task:"+op.Tasks[0].ID {
			for _, omitted := range []string{"started_at", "finished_at", "agent_id", "command_id", "step_id", "error"} {
				if _, exists := data[omitted]; exists {
					t.Fatalf("queued node should omit %q: %+v", omitted, data)
				}
			}
		}
	}
	if repeated := server.request(t, http.MethodGet, path, authenticate); repeated.body != result.body {
		t.Fatalf("equal snapshots changed graph IDs/order: first=%s repeated=%s", result.body, repeated.body)
	}
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) || !reflect.DeepEqual(artifactsBefore, artifactStorageFiles(t, fixture)) {
		t.Fatal("graph reads wrote operation or artifact storage")
	}

	// A subsequent HTTP read must observe domain-store changes, without a restart.
	added := operations.NewTask(op.ID, "", "", actions.NextAction{ActionName: "review"})
	if _, err := store.QueueTask(t.Context(), op.ID, added); err != nil {
		t.Fatal(err)
	}
	started := op.UpdatedAt.Add(time.Hour)
	updated, err := store.DispatchTask(t.Context(), op.ID, operations.TaskDispatch{TaskID: added.ID, AgentID: "review-agent-1234abcd", StartedAt: started, DeadlineAt: started.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	before = operationBytes(t, fixture, op.ID)
	fresh := decodeResponse[models.OperationGraphResponse](t, server.request(t, http.MethodGet, path, authenticate))
	if len(fresh.Nodes) != len(graph.Nodes)+1 || len(fresh.Edges) != len(graph.Edges) || fresh.Status != string(updated.Status) || !fresh.UpdatedAt.Equal(updated.UpdatedAt) {
		t.Fatalf("graph did not observe updated store snapshot: %+v", fresh)
	}
	found := false
	for _, node := range fresh.Nodes {
		if node.ID == "task:"+added.ID {
			found = node.Kind == "task" && node.Task != nil && node.Task.Status == string(operations.TaskRunning) && node.Task.StartedAt != nil && node.Task.StartedAt.Equal(started)
		} else if !reflect.DeepEqual(node, nodes[node.ID]) {
			t.Fatalf("existing node identity/data changed after appending a task: %+v", node)
		}
	}
	if !found || !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("fresh graph omitted the dispatched task or rewrote operation storage")
	}
}

func TestOperationGraphEmptySnapshotUsesArrays(t *testing.T) {
	fixture := newFixture(t)
	op := operationRecord(1)
	op.Status = operations.Completed
	op = saveOperation(t, operationStore(t, fixture), op)
	server := startAPI(t, append(fixture.localArgs(), "--swagger"))
	before := operationBytes(t, fixture, op.ID)
	result := server.request(t, http.MethodGet, "/api/v1/operations/"+op.ID+"/graph", authenticate)
	graph := decodeResponse[models.OperationGraphResponse](t, result)
	wire := decodeResponse[map[string]json.RawMessage](t, result)
	if graph.OperationID != op.ID || graph.Status != string(op.Status) || !graph.UpdatedAt.Equal(op.UpdatedAt) || len(wire) != 5 || string(wire["nodes"]) != "[]" || string(wire["edges"]) != "[]" {
		t.Fatalf("empty graph lost snapshot fields or arrays: %s", result.body)
	}
	assertResponseSchema(t, server, "OperationGraph", result)
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("empty graph query rewrote operation storage")
	}
}

func TestOperationGraphProtectsReadsAndMapsErrors(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), operationRecord(1))
	server := startAPI(t, fixture.localArgs())
	path := "/api/v1/operations/" + op.ID + "/graph"
	before := operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodGet, path), http.StatusUnauthorized, "authentication_required")
	assertAPIError(t, server.request(t, http.MethodGet, path, authenticate, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }), http.StatusForbidden, "cross_origin_request")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		result := server.request(t, method, path, authenticate)
		if result.status != http.StatusMethodNotAllowed {
			t.Fatalf("%s graph returned %d: %s", method, result.status, result.body)
		}
		assertSecurityHeaders(t, result, false)
	}
	assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/operations/"+recordID(999)+"/graph", authenticate), http.StatusNotFound, "operation_not_found")
	for _, invalid := range []string{"not-a-uuid", strings.ToUpper(recordID(0xabcdef)), strings.ReplaceAll(op.ID, "-", ""), "%20" + op.ID} {
		assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/operations/"+invalid+"/graph", authenticate), http.StatusBadRequest, "invalid_operation_id")
	}
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("rejected graph queries changed operation storage")
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "database", "operations", op.ID+".json"), []byte("private-operation-content-not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	before = operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodGet, path, authenticate), http.StatusInternalServerError, "internal_error")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("graph query repaired corrupt operation storage")
	}
	assertHealthy(t, server.request(t, http.MethodGet, "/healthz"))
}
