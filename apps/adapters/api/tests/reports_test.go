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

	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	fsreports "commander/providers/fs/reports"
)

func TestReportsReturnFullSavedRecordsWithoutController(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	// No controller consumer is started: both routes must read durable stores.
	server := startAPI(t, fixture.localArgs())
	for index, output := range []string{`{"summary":"Saved report","links":3}`, `null`, `["page",2]`, `"reviewed"`, `42`, `true`} {
		t.Run(output, func(t *testing.T) {
			op := operationRecord(index + 1)
			op.Status, op.ReportID = operations.Completed, recordID(index+100)
			// The report store is authoritative even if operation output differs.
			op.Output = json.RawMessage(`{"summary":"Operation output"}`)
			op = saveOperation(t, store, op)
			report := saveReport(t, fixture, reports.Report{ID: op.ReportID, OperationID: op.ID, Mission: op.Mission.Name, Output: json.RawMessage(output), CreatedAt: op.UpdatedAt})
			operationBefore, reportsBefore := operationBytes(t, fixture, op.ID), completionReportFiles(t, fixture)

			for _, path := range []string{"/api/v1/reports/" + report.ID, "/api/v1/operations/" + op.ID + "/report"} {
				result := server.request(t, http.MethodGet, path, authenticate)
				got := decodeResponse[reports.Report](t, result)
				if !reflect.DeepEqual(got, report) {
					t.Fatalf("report lookup changed the saved record: got %+v, want %+v", got, report)
				}
				assertSecurityHeaders(t, result, false)
			}
			if !bytes.Equal(operationBefore, operationBytes(t, fixture, op.ID)) || !reflect.DeepEqual(reportsBefore, completionReportFiles(t, fixture)) {
				t.Fatal("report reads changed operation or report storage")
			}
		})
	}

	// Direct lookup does not require a corresponding operation record.
	orphan := saveReport(t, fixture, reports.Report{ID: recordID(200), OperationID: recordID(900), Mission: "archived-mission", Output: json.RawMessage(`{"archived":true}`), CreatedAt: operationRecord(1).CreatedAt})
	if got := decodeResponse[reports.Report](t, server.request(t, http.MethodGet, "/api/v1/reports/"+orphan.ID, authenticate)); !reflect.DeepEqual(got, orphan) {
		t.Fatalf("direct report lookup requires operation state or changed the report: %+v", got)
	}
}

func TestReportQueriesValidateIDsAndMissingRecords(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	server := startAPI(t, fixture.localArgs())
	assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/reports/"+recordID(999), authenticate), http.StatusNotFound, "report_not_found")
	assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/operations/"+recordID(999)+"/report", authenticate), http.StatusNotFound, "operation_not_found")
	for _, invalid := range []string{"not-a-uuid", strings.ToUpper(recordID(0xabcdef)), strings.ReplaceAll(recordID(1), "-", ""), "%20" + recordID(1)} {
		assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/reports/"+invalid, authenticate), http.StatusBadRequest, "invalid_report_id")
		assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/operations/"+invalid+"/report", authenticate), http.StatusBadRequest, "invalid_operation_id")
	}
	for index, tc := range []struct {
		name      string
		status    operations.Status
		reference bool
		persisted bool
	}{
		{"planning-with-report", operations.Planning, true, true},
		{"failed-with-report", operations.Failed, true, true},
		{"completed-without-reference", operations.Completed, false, true},
		{"completed-missing-report", operations.Completed, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := operationRecord(index + 1)
			op.Status = tc.status
			report := reports.Report{ID: recordID(index + 100), OperationID: op.ID, Mission: op.Mission.Name, Output: json.RawMessage(`{"private":"saved output"}`), CreatedAt: op.UpdatedAt}
			if tc.reference {
				op.ReportID = report.ID
			}
			if tc.persisted {
				saveReport(t, fixture, report)
			}
			saveOperation(t, store, op)
			before, reportsBefore := operationBytes(t, fixture, op.ID), completionReportFiles(t, fixture)
			assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/operations/"+op.ID+"/report", authenticate), http.StatusNotFound, "report_not_found")
			if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) || !reflect.DeepEqual(reportsBefore, completionReportFiles(t, fixture)) {
				t.Fatal("unsuccessful report lookup changed persisted records")
			}
		})
	}
}

func TestReportQueriesKeepAuthenticationOriginAndMethodProtections(t *testing.T) {
	fixture := newFixture(t)
	op := operationRecord(1)
	op.Status, op.ReportID = operations.Completed, recordID(100)
	saveOperation(t, operationStore(t, fixture), op)
	saveReport(t, fixture, reports.Report{ID: op.ReportID, OperationID: op.ID, Mission: op.Mission.Name, Output: json.RawMessage(`{"private":"saved output"}`), CreatedAt: op.UpdatedAt})
	server := startAPI(t, fixture.localArgs())
	before, reportsBefore := operationBytes(t, fixture, op.ID), completionReportFiles(t, fixture)
	for _, path := range []string{"/api/v1/reports/" + op.ReportID, "/api/v1/operations/" + op.ID + "/report"} {
		t.Run(path, func(t *testing.T) {
			unauthorized := server.request(t, http.MethodGet, path)
			assertAPIError(t, unauthorized, http.StatusUnauthorized, "authentication_required")
			assertSecurityHeaders(t, unauthorized, false)
			forbidden := server.request(t, http.MethodGet, path, authenticate, func(request *http.Request) { request.Header.Set("Origin", "https://attacker.invalid") })
			assertAPIError(t, forbidden, http.StatusForbidden, "cross_origin_request")
			assertSecurityHeaders(t, forbidden, false)
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
				result := server.request(t, method, path, authenticate)
				if result.status != http.StatusMethodNotAllowed {
					t.Fatalf("%s returned %d, want 405: %s", method, result.status, result.body)
				}
				assertSecurityHeaders(t, result, false)
			}
		})
	}
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) || !reflect.DeepEqual(reportsBefore, completionReportFiles(t, fixture)) {
		t.Fatal("rejected report requests changed persisted records")
	}
}

func TestReportQueriesRejectMismatchedReferencesAndCorruptStorage(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	server := startAPI(t, fixture.localArgs())
	for index, tc := range []struct {
		name   string
		change func(*reports.Report)
	}{
		{"wrong-operation", func(report *reports.Report) { report.OperationID = recordID(999) }},
		{"wrong-mission", func(report *reports.Report) { report.Mission = "other-private-mission" }},
		{"corrupt-report", nil},
		{"corrupt-operation", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := operationRecord(index + 1)
			op.Status, op.ReportID = operations.Completed, recordID(index+100)
			saveOperation(t, store, op)
			report := reports.Report{ID: op.ReportID, OperationID: op.ID, Mission: op.Mission.Name, Output: json.RawMessage(`{"private":"saved output"}`), CreatedAt: op.UpdatedAt}
			if tc.change != nil {
				tc.change(&report)
			}
			saveReport(t, fixture, report)
			if tc.name == "corrupt-report" || tc.name == "corrupt-operation" {
				path := filepath.Join(fixture.root, "storage", "reports", report.ID+".json")
				if tc.name == "corrupt-operation" {
					path = filepath.Join(fixture.root, "database", "operations", op.ID+".json")
				}
				if err := os.WriteFile(path, []byte("private-storage-content-not-json"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, reportsBefore := operationBytes(t, fixture, op.ID), completionReportFiles(t, fixture)
			// Exact public error envelopes must not disclose any report or storage data.
			assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/operations/"+op.ID+"/report", authenticate), http.StatusInternalServerError, "internal_error")
			if tc.name == "corrupt-report" {
				assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/reports/"+report.ID, authenticate), http.StatusInternalServerError, "internal_error")
			} else if got := decodeResponse[reports.Report](t, server.request(t, http.MethodGet, "/api/v1/reports/"+report.ID, authenticate)); !reflect.DeepEqual(got, report) {
				t.Fatalf("direct lookup must read the report independently of operation state: %+v", got)
			}
			if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) || !reflect.DeepEqual(reportsBefore, completionReportFiles(t, fixture)) {
				t.Fatal("report queries repaired or rewrote inconsistent stored data")
			}
		})
	}
	assertHealthy(t, server.request(t, http.MethodGet, "/healthz"))
}

func TestCompletedOperationReportIsAvailableThroughBothRoutes(t *testing.T) {
	fixture := newFixture(t)
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	op := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(`{"mission_name":"any-input","input":{}}`, "application/json")), http.StatusCreated)
	base := "/api/v1/operations/" + op.ID
	const output = `{"summary":"Completed through HTTP","links":3}`
	completed := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, base+"/complete", authenticate, commandBody(`{"output":`+output+`}`, "application/json")))
	byOperation := decodeResponse[reports.Report](t, server.request(t, http.MethodGet, base+"/report", authenticate))
	byID := decodeResponse[reports.Report](t, server.request(t, http.MethodGet, "/api/v1/reports/"+completed.ReportID, authenticate))
	if completed.Status != operations.Completed || byOperation.ID != completed.ReportID || byOperation.OperationID != op.ID || byOperation.Mission != op.Mission.Name || byOperation.CreatedAt.IsZero() || string(byOperation.Output) != output || !reflect.DeepEqual(byOperation, byID) {
		t.Fatalf("completed report identity or output differs between routes: operation=%+v byOperation=%+v byID=%+v", completed, byOperation, byID)
	}
}

func saveReport(t *testing.T, fixture fixture, report reports.Report) reports.Report {
	t.Helper()
	store, err := fsreports.NewReportFsStore(filepath.Join(fixture.root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(t.Context(), report.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
