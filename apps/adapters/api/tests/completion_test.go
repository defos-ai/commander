package api_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	"commander/framework/id"
	fsreports "commander/providers/fs/reports"
)

func TestOperationCompletionPersistsReportAndResolvesModelDecision(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), continuationOperationRecord(1))
	execution := startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID
	before := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))
	const output = `{"summary":"All links reviewed","links":3}`

	completed := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, base+"/complete", authenticate, commandBody(`{"output":`+output+`}`, "application/json; charset=utf-8")))
	stored := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))
	if completed.ID != op.ID || completed.Status != operations.Completed || !id.Valid(completed.ReportID) || string(completed.Output) != output || !reflect.DeepEqual(completed, stored) {
		t.Fatalf("completion must return the full persisted operation with output and report identity: response=%+v saved=%+v", completed, stored)
	}
	if len(completed.Tasks) != 1 || completed.Tasks[0].Continuation.Status != operations.ContinuationResolved || completed.Tasks[0].Continuation.Version != before.Tasks[0].Continuation.Version+1 || !reflect.DeepEqual(completed.Tasks[0].Result, before.Tasks[0].Result) {
		t.Fatalf("completion must resolve the model decision and retain its task result: %+v", completed.Tasks)
	}
	if len(completed.Events) != len(before.Events)+1 || !reflect.DeepEqual(completed.Events[:len(before.Events)], before.Events) || completed.Events[len(before.Events)].Type != operations.OperationCompleted {
		t.Fatalf("completion must append its event while retaining history: %+v", completed.Events)
	}
	report, err := execution.GetReport(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.ID != completed.ReportID || report.OperationID != op.ID || report.Mission != op.Mission.Name || string(report.Output) != output || report.CreatedAt.IsZero() {
		t.Fatalf("completion report lost its identity, output, or creation time: %+v", report)
	}
	// A separate store must read the durable report without the runner's state.
	reportStore, err := fsreports.NewReportFsStore(filepath.Join(fixture.root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reportStore.Get(t.Context(), completed.ReportID)
	if err != nil || !reflect.DeepEqual(persisted, report) {
		t.Fatalf("report was not persisted: report=%+v error=%v", persisted, err)
	}
	operationBefore := operationBytes(t, fixture, op.ID)
	reportsBefore := completionReportFiles(t, fixture)
	assertAPIError(t, server.request(t, http.MethodPost, base+"/complete", authenticate, commandBody(`{"output":{"summary":"replace"}}`, "application/json")), http.StatusConflict, "operation_finished")
	assertAPIError(t, server.request(t, http.MethodPost, base+"/fail", authenticate, commandBody(`{"reason":"replace"}`, "application/json")), http.StatusConflict, "operation_finished")
	if len(reportsBefore) != 1 || !bytes.Equal(operationBefore, operationBytes(t, fixture, op.ID)) || !reflect.DeepEqual(reportsBefore, completionReportFiles(t, fixture)) {
		t.Fatal("terminal command replay changed the operation or created/replaced a report")
	}
}

func TestOperationCompletionAcceptsEveryJSONOutputAllowedByMission(t *testing.T) {
	fixture := newFixture(t)
	execution := startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	for _, output := range []string{`null`, `{}`, `["page",2]`, `"reviewed"`, `42`, `true`} {
		t.Run(output, func(t *testing.T) {
			op := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(`{"mission_name":"any-input","input":{}}`, "application/json")), http.StatusCreated)
			completed := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, "/api/v1/operations/"+op.ID+"/complete", authenticate, commandBody(`{"output":`+output+`}`, "application/json")))
			report, err := execution.GetReport(t.Context(), op.ID)
			if err != nil || completed.Status != operations.Completed || !id.Valid(completed.ReportID) || string(completed.Output) != output || report.ID != completed.ReportID || string(report.Output) != output {
				t.Fatalf("completion must preserve schema-valid JSON, including explicit null: operation=%+v report=%+v error=%v", completed, report, err)
			}
		})
	}
}

func TestOperationCompletionRejectsBlockedOrInvalidOutputWithoutReports(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	cases := []struct {
		name   string
		change func(*operations.Operation)
		output string
		status int
		code   string
	}{
		{"queued-task", func(op *operations.Operation) {
			op.Tasks = append(op.Tasks, operations.Task{ID: recordID(30), CommandID: recordID(30), OperationID: op.ID, ActionName: "review", Status: operations.TaskQueued})
		}, `{}`, http.StatusConflict, "operation_active_tasks"},
		{"pending-question", func(op *operations.Operation) {
			op.Questions = []operations.Question{{ID: recordID(40), Status: operations.QuestionPending, Text: "Continue?", AskedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}}
		}, `{}`, http.StatusConflict, "question_pending"},
		{"automatic-work", func(op *operations.Operation) {
			op.Tasks[0].Continuation.DecisionBy = operations.DecisionCode
		}, `{}`, http.StatusConflict, "operation_unresolved_work"},
		{"operator-work", func(op *operations.Operation) {
			op.Tasks[0].Continuation.DecisionBy = operations.DecisionUser
		}, `{}`, http.StatusConflict, "operation_unresolved_work"},
		{"invalid-schema", func(op *operations.Operation) {
			op.Mission.OutputSchema = json.RawMessage(`{"type":"object","properties":{"links":{"type":"integer"}},"required":["links"]}`)
		}, `{"links":"three"}`, http.StatusUnprocessableEntity, "invalid_output"},
		{"schema-rejects-null", func(op *operations.Operation) {}, `null`, http.StatusUnprocessableEntity, "invalid_output"},
		{"invalid-persisted-input", func(op *operations.Operation) {
			op.Input = json.RawMessage(`null`)
		}, `{}`, http.StatusInternalServerError, "internal_error"},
		{"invalid-persisted-output-schema", func(op *operations.Operation) {
			op.Mission.OutputSchema = json.RawMessage(`{"type":"unsupported"}`)
		}, `{}`, http.StatusInternalServerError, "internal_error"},
	}
	for index, tc := range cases {
		op := continuationOperationRecord(index + 1)
		tc.change(&op)
		saveOperation(t, store, op)
	}
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operationID := recordID(index + 1)
			before := operationBytes(t, fixture, operationID)
			assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations/"+operationID+"/complete", authenticate, commandBody(`{"output":`+tc.output+`}`, "application/json")), tc.status, tc.code)
			if !bytes.Equal(before, operationBytes(t, fixture, operationID)) || len(completionReportFiles(t, fixture)) != 0 {
				t.Fatal("rejected completion changed the operation or published a report")
			}
		})
	}
}

func TestOperationFailureCancelsQueuedTasksAndPreservesResults(t *testing.T) {
	fixture := newFixture(t)
	op := continuationOperationRecord(1)
	op.Tasks = append(op.Tasks, operations.Task{ID: recordID(30), CommandID: recordID(30), OperationID: op.ID, ActionName: "review", Status: operations.TaskQueued})
	op = saveOperation(t, operationStore(t, fixture), op)
	execution := startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID
	before := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))
	const reason = "Review cannot continue.\nThe destination is unavailable."

	failed := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, base+"/fail", authenticate, commandBody(fmt.Sprintf(`{"reason":%q}`, reason), "application/json")))
	stored := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))
	if failed.ID != op.ID || failed.Status != operations.Failed || failed.Error != reason || failed.ReportID != "" || len(failed.Output) != 0 || !reflect.DeepEqual(failed, stored) {
		t.Fatalf("failure must return the full persisted operation and original reason: response=%+v saved=%+v", failed, stored)
	}
	if len(failed.Tasks) != 2 || !reflect.DeepEqual(failed.Tasks[0], before.Tasks[0]) || failed.Tasks[1].Status != operations.TaskCancelled || failed.Tasks[1].FinishedAt == nil || failed.Tasks[1].AgentID != "" || failed.Tasks[1].Result != nil {
		t.Fatalf("failure must preserve completed work and cancel the queued task: %+v", failed.Tasks)
	}
	if len(failed.Events) != len(before.Events)+1 || failed.Events[len(before.Events)].Type != operations.OperationFailed || failed.Events[len(before.Events)].Message != reason {
		t.Fatalf("failure must append its reason to the operation history: %+v", failed.Events)
	}
	if _, err := execution.GetReport(t.Context(), op.ID); !errors.Is(err, reports.ErrUnknownReport) {
		t.Fatalf("failed operation must have no report: %v", err)
	}
	operationBefore := operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodPost, base+"/fail", authenticate, commandBody(`{"reason":"replacement reason"}`, "application/json")), http.StatusConflict, "operation_finished")
	assertAPIError(t, server.request(t, http.MethodPost, base+"/complete", authenticate, commandBody(`{"output":{}}`, "application/json")), http.StatusConflict, "operation_finished")
	if !bytes.Equal(operationBefore, operationBytes(t, fixture, op.ID)) || len(completionReportFiles(t, fixture)) != 0 {
		t.Fatal("failure or its replay published a report or changed terminal state")
	}
}

func TestOperationCompletionAndFailureRejectInvalidRequestsWithoutMutation(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), continuationOperationRecord(1))
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	before := operationBytes(t, fixture, op.ID)
	for _, tc := range []struct {
		suffix  string
		body    string
		invalid []string
	}{
		{"/complete", `{"output":{}}`, []string{`{}`, `{"output":{},"reason":"ignored"}`, `{"output":{},"operation_id":"ignored"}`, `{"output":{},"report_id":"ignored"}`}},
		{"/fail", `{"reason":"Cannot continue"}`, []string{`{}`, `{"reason":""}`, `{"reason":" \n\t"}`, `{"reason":null}`, `{"reason":17}`, `{"reason":{}}`, `{"reason":"stop","output":{}}`, `{"reason":"stop","operation_id":"ignored"}`}},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			path := "/api/v1/operations/" + op.ID + tc.suffix
			assertAPIError(t, server.request(t, http.MethodPost, path), http.StatusUnauthorized, "authentication_required")
			for _, body := range append(tc.invalid, ``, `{`, `{} {}`, `{} null`, `null`, `[]`, `"text"`, `17`, `true`, `{"unknown":true}`) {
				assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_request")
			}
			assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(tc.body, "text/plain")), http.StatusUnsupportedMediaType, "unsupported_media_type")
			assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(`{"padding":"`+strings.Repeat("x", 1<<20)+`"}`, "application/json")), http.StatusRequestEntityTooLarge, "request_too_large")
			for _, invalid := range []string{"not-a-uuid", strings.ToUpper(recordID(0xabcdef)), strings.ReplaceAll(op.ID, "-", "")} {
				assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations/"+invalid+tc.suffix, authenticate, commandBody(tc.body, "application/json")), http.StatusBadRequest, "invalid_operation_id")
			}
			assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations/"+recordID(999)+tc.suffix, authenticate, commandBody(tc.body, "application/json")), http.StatusNotFound, "operation_not_found")
		})
	}
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) || len(completionReportFiles(t, fixture)) != 0 {
		t.Fatal("invalid terminal commands changed the operation or published a report")
	}
}

func TestOperationCompletionAndFailureRejectRecoveredTerminalOperations(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	statuses := []operations.Status{operations.Completed, operations.Failed, operations.Cancelled}
	for index, status := range statuses {
		op := operationRecord(index + 1)
		op.Status = status
		saveOperation(t, store, op)
	}
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	for index, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			operationID := recordID(index + 1)
			base := "/api/v1/operations/" + operationID
			before := operationBytes(t, fixture, operationID)
			assertAPIError(t, server.request(t, http.MethodPost, base+"/complete", authenticate, commandBody(`{"output":{}}`, "application/json")), http.StatusConflict, "operation_finished")
			assertAPIError(t, server.request(t, http.MethodPost, base+"/fail", authenticate, commandBody(`{"reason":"Cannot continue"}`, "application/json")), http.StatusConflict, "operation_finished")
			if !bytes.Equal(before, operationBytes(t, fixture, operationID)) || len(completionReportFiles(t, fixture)) != 0 {
				t.Fatal("terminal commands changed a recovered finished operation or published a report")
			}
		})
	}
}

func completionReportFiles(t *testing.T, fixture fixture) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixture.root, "storage", "reports", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(path)] = string(data)
	}
	return files
}
