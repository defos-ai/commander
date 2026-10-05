package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	fsoperations "commander/providers/fs/operations"
)

func TestOperationsReturnCompleteSavedRecordsInCreationOrder(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	older := operationRecord(1)
	older.Status, older.Error = operations.Failed, "Review could not finish"
	newer := operationRecord(3)
	newer.CreatedAt = older.CreatedAt.Add(time.Hour)
	newer.Status = operations.Completed
	newer.Preflight = &operations.Preflight{Ready: true, Version: 4, Actions: []operations.ActionReadiness{{ActionName: "review", ReadyAgentIDs: []string{"review-agent-1234abcd"}}}}
	newer.Output, newer.ReportID = json.RawMessage(`{"summary":"Review complete"}`), recordID(400)
	newer.Tasks = []operations.Task{taskRecord(newer.ID, 20)}
	newer.Questions = []operations.Question{{ID: recordID(30), Status: operations.QuestionAnswered, Text: "Include links?", Options: []string{"yes", "no"}, Answer: "yes", AskedAt: newer.CreatedAt, AnsweredAt: &newer.UpdatedAt, ExpiresAt: newer.UpdatedAt.Add(time.Hour)}}
	newer.Events = []operations.Event{{Sequence: 1, OperationID: newer.ID, Type: operations.OperationCompleted, Status: operations.Completed, Message: "Review complete", CreatedAt: newer.UpdatedAt}}
	tied := operationRecord(2)
	tied.CreatedAt = newer.CreatedAt
	older, newer, tied = saveOperation(t, store, older), saveOperation(t, store, newer), saveOperation(t, store, tied)
	server := startAPI(t, fixture.localArgs())
	before := operationBytes(t, fixture, newer.ID)

	list := decodeResponse[struct {
		Items []operations.Operation `json:"items"`
	}](t, server.request(t, http.MethodGet, "/api/v1/operations", authenticate))
	detail := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, "/api/v1/operations/"+newer.ID, authenticate))

	if !reflect.DeepEqual(list.Items, []operations.Operation{tied, newer, older}) {
		t.Fatalf("operation list did not preserve full records in creation/ID order: %+v", list.Items)
	}
	if !reflect.DeepEqual(detail, newer) {
		t.Fatalf("operation detail changed the saved record: got %+v, want %+v", detail, newer)
	}
	if !bytes.Equal(before, operationBytes(t, fixture, newer.ID)) {
		t.Fatal("operation queries rewrote the stored record")
	}
	// A later request observes records saved after the API started.
	live := operationRecord(4)
	live.CreatedAt = newer.CreatedAt.Add(time.Hour)
	live = saveOperation(t, store, live)
	updated := decodeResponse[struct {
		Items []operations.Operation `json:"items"`
	}](t, server.request(t, http.MethodGet, "/api/v1/operations", authenticate))
	if len(updated.Items) != 4 || !reflect.DeepEqual(updated.Items[0], live) {
		t.Fatalf("operation list did not observe live storage: %+v", updated.Items)
	}
}

func TestOperationQueriesAuthenticateAndRejectUnsupportedMethods(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), operationRecord(1))
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID
	paths := []string{"/api/v1/operations", base, base + "/tasks", base + "/tasks/" + recordID(20), base + "/events", base + "/questions", base + "/questions/" + recordID(30)}
	before := operationBytes(t, fixture, op.ID)

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			assertAPIError(t, server.request(t, http.MethodGet, path), http.StatusUnauthorized, "authentication_required")
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
				result := server.request(t, method, path, authenticate)
				if method == http.MethodPost && (path == "/api/v1/operations" || path == base+"/tasks" || path == base+"/questions") {
					assertAPIError(t, result, http.StatusBadRequest, "invalid_request")
					continue
				}
				if result.status != http.StatusMethodNotAllowed {
					t.Fatalf("%s returned %d, want 405: %s", method, result.status, result.body)
				}
			}
		})
	}

	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("rejected requests changed operation storage")
	}
}

func TestOperationQueryMissingRecordsAndInvalidIDs(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	op := saveOperation(t, store, operationRecord(1))
	other := operationRecord(2)
	other.Tasks = []operations.Task{taskRecord(other.ID, 20)}
	other.Questions = []operations.Question{{ID: recordID(30), Status: operations.QuestionPending, Text: "Continue?"}}
	saveOperation(t, store, other)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID

	for _, suffix := range []string{"", "/tasks", "/tasks/" + other.Tasks[0].ID, "/events", "/questions", "/questions/" + other.Questions[0].ID} {
		assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/operations/"+recordID(999)+suffix, authenticate), http.StatusNotFound, "operation_not_found")
	}
	for _, taskID := range []string{other.Tasks[0].ID, recordID(998)} {
		assertAPIError(t, server.request(t, http.MethodGet, base+"/tasks/"+taskID, authenticate), http.StatusNotFound, "task_not_found")
		assertAPIError(t, server.request(t, http.MethodGet, base+"/tasks?cursor="+taskID, authenticate), http.StatusNotFound, "task_not_found")
	}
	for _, questionID := range []string{other.Questions[0].ID, recordID(997)} {
		assertAPIError(t, server.request(t, http.MethodGet, base+"/questions/"+questionID, authenticate), http.StatusNotFound, "question_not_found")
	}
	for _, invalid := range []string{"not-a-uuid", strings.ToUpper(recordID(0xabcdef)), strings.ReplaceAll(op.ID, "-", ""), "%20" + op.ID} {
		for _, suffix := range []string{"", "/tasks", "/tasks/" + recordID(20), "/events", "/questions", "/questions/" + recordID(30)} {
			assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/operations/"+invalid+suffix, authenticate), http.StatusBadRequest, "invalid_operation_id")
		}
		assertAPIError(t, server.request(t, http.MethodGet, base+"/tasks/"+invalid, authenticate), http.StatusBadRequest, "invalid_task_id")
		assertAPIError(t, server.request(t, http.MethodGet, base+"/questions/"+invalid, authenticate), http.StatusBadRequest, "invalid_question_id")
	}
}

func TestOperationQueryEmptyCollectionsAndStorageErrors(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	server := startAPI(t, fixture.localArgs())

	list := decodeResponse[map[string]json.RawMessage](t, server.request(t, http.MethodGet, "/api/v1/operations", authenticate))
	if len(list) != 1 || string(list["items"]) != "[]" {
		t.Fatalf("empty operations must be an array: %+v", list)
	}
	op := saveOperation(t, store, operationRecord(1))
	base := "/api/v1/operations/" + op.ID
	for _, suffix := range []string{"/tasks", "/events"} {
		page := decodeResponse[map[string]json.RawMessage](t, server.request(t, http.MethodGet, base+suffix, authenticate))
		if len(page) != 2 || string(page["items"]) != "[]" || string(page["next_cursor"]) != `""` {
			t.Fatalf("empty %s page lost its empty cursor or array: %+v", suffix, page)
		}
	}
	questions := decodeResponse[map[string]json.RawMessage](t, server.request(t, http.MethodGet, base+"/questions", authenticate))
	if len(questions) != 3 || string(questions["items"]) != "[]" || string(questions["last_question"]) != "null" || string(questions["remaining_questions"]) != "0" {
		t.Fatalf("empty questions response has the wrong shape: %+v", questions)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "database", "operations", op.ID+".json"), []byte("private-operation-content-not-json"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/v1/operations", base, base + "/tasks", base + "/tasks/" + recordID(20), base + "/events", base + "/questions", base + "/questions/" + recordID(30)} {
		assertAPIError(t, server.request(t, http.MethodGet, path, authenticate), http.StatusInternalServerError, "internal_error")
	}
	assertHealthy(t, server.request(t, http.MethodGet, "/healthz"))
}

func operationStore(t *testing.T, fixture fixture) *fsoperations.OperationFsStore {
	t.Helper()
	store, err := fsoperations.NewOperationFsStore(filepath.Join(fixture.root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func saveOperation(t *testing.T, store *fsoperations.OperationFsStore, op operations.Operation) operations.Operation {
	t.Helper()
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func operationRecord(number int) operations.Operation {
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	return operations.Operation{ID: recordID(number), Status: operations.Planning,
		Mission: missions.Mission{Name: "review", Objective: "Review website links", RequiredActions: []string{"review"}, StartingActions: []string{"review"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)},
		Input:   json.RawMessage(`{"url":"https://example.test"}`), CreatedAt: created, UpdatedAt: created.Add(time.Minute)}
}

func taskRecord(operationID string, number int) operations.Task {
	started := time.Date(2026, time.January, 2, 3, 5, 5, 0, time.UTC)
	finished, deadline := started.Add(time.Minute), started.Add(time.Hour)
	return operations.Task{ID: recordID(number), OperationID: operationID, ParentTaskID: recordID(900), StepID: "review-links", AgentID: "review-agent-1234abcd", ActionName: "review", CommandID: recordID(number), DeadlineAt: &deadline,
		Input: json.RawMessage(`{"url":"https://example.test"}`), Status: operations.TaskSucceeded, StartedAt: started, FinishedAt: &finished,
		Result:       &actions.ActionOutput{ActionName: "review", Output: json.RawMessage(`{"links":3}`), NextActions: []actions.NextAction{{ActionName: "summarize", Input: json.RawMessage(`{"links":3}`)}}},
		Continuation: &operations.Continuation{Version: 2, DecisionBy: operations.DecisionModel, Status: operations.ContinuationPending, ProposedActions: []actions.NextAction{{ActionName: "summarize", Input: json.RawMessage(`{"links":3}`)}}, CreatedTaskIDs: []string{recordID(901)}}}
}

func recordID(number int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", number)
}

func operationBytes(t *testing.T, fixture fixture, operationID string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture.root, "database", "operations", operationID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
