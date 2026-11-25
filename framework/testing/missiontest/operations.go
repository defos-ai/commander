package missiontest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
)

type Assignment struct {
	ActionName    string                   `json:"action_name"`
	Input         json.RawMessage          `json:"input,omitempty"`
	SessionTaskID string                   `json:"session_task_id,omitempty"`
	Selector      *operations.TaskSelector `json:"selector,omitempty"`
}

// Start reserves a caller-owned identity for cleanup before submitting once.
// A lost response does not authorize another start.
func (d *Driver) Start(missionName string, input any) operations.Operation {
	d.t.Helper()
	operationID := id.New()
	d.t.Cleanup(func() { d.cleanup(operationID) })
	request := struct {
		OperationID string `json:"operation_id"`
		MissionName string `json:"mission_name"`
		Input       any    `json:"input"`
	}{operationID, missionName, input}
	return d.readOperation(http.MethodPost, "/api/v1/operations", operationID, request, http.StatusCreated)
}

func (d *Driver) Assign(operationID string, assignment Assignment) operations.Task {
	d.t.Helper()
	path := d.operationPath(operationID) + "/tasks"
	task := read[operations.Task](d, http.MethodPost, path, assignment, http.StatusCreated)
	if !id.Valid(task.ID) || task.OperationID != operationID {
		d.t.Fatal("missiontest: assignment response does not identify the requested operation and task")
	}
	return task
}

func (d *Driver) Get(operationID string) operations.Operation {
	d.t.Helper()
	return d.readOperation(http.MethodGet, d.operationPath(operationID), operationID, nil, http.StatusOK)
}

// Resolve submits the continuation version observed by the caller. No next
// actions explicitly finishes this review, without resolving any other review.
func (d *Driver) Resolve(operationID string, task operations.Task, next ...actions.NextAction) operations.Operation {
	d.t.Helper()
	if !id.Valid(task.ID) || task.OperationID != operationID || task.Continuation == nil ||
		task.Continuation.Status != operations.ContinuationPending || task.Continuation.Version == 0 {
		d.t.Fatal("missiontest: resolve requires a pending continuation from this operation")
	}
	for _, action := range next {
		if action.SessionIntent != nil {
			d.t.Fatal("missiontest: continuation session intent belongs to the controller")
		}
	}
	if next == nil {
		next = []actions.NextAction{}
	}
	request := struct {
		Version uint64               `json:"version"`
		Actions []actions.NextAction `json:"actions"`
	}{task.Continuation.Version, next}
	path := d.operationPath(operationID) + "/tasks/" + url.PathEscape(task.ID) + "/continuation/resolve"
	return d.readOperation(http.MethodPost, path, operationID, request, http.StatusOK)
}

// Answer uses the API's operator authority, without simulating a model answer.
func (d *Driver) Answer(operationID, questionID, answer string) operations.Operation {
	d.t.Helper()
	if !id.Valid(questionID) {
		d.t.Fatal("missiontest: question ID must be a canonical UUID")
	}
	request := struct {
		Answer string `json:"answer"`
	}{answer}
	path := d.operationPath(operationID) + "/questions/" + url.PathEscape(questionID) + "/answer"
	return d.readOperation(http.MethodPost, path, operationID, request, http.StatusOK)
}

// Complete submits an explicit operator completion, preserving mission checks.
func (d *Driver) Complete(operationID string, output any) operations.Operation {
	d.t.Helper()
	request := struct {
		Output any `json:"output"`
	}{output}
	return d.readOperation(http.MethodPost, d.operationPath(operationID)+"/complete", operationID, request, http.StatusOK)
}

func (d *Driver) Cancel(operationID string) operations.Operation {
	d.t.Helper()
	return d.readOperation(http.MethodPost, d.operationPath(operationID)+"/cancel", operationID, nil, http.StatusOK)
}

func (d *Driver) Report(operationID string) reports.Report {
	d.t.Helper()
	report := read[reports.Report](d, http.MethodGet, d.operationPath(operationID)+"/report", nil, http.StatusOK)
	if report.OperationID != operationID || report.Validate() != nil {
		d.t.Fatal("missiontest: report response does not contain a valid report for this operation")
	}
	return report
}

func (d *Driver) operationPath(operationID string) string {
	d.t.Helper()
	if !id.Valid(operationID) {
		d.t.Fatal("missiontest: operation ID must be a canonical UUID")
	}
	return "/api/v1/operations/" + url.PathEscape(operationID)
}

func (d *Driver) readOperation(method, path, operationID string, input any, status int) operations.Operation {
	d.t.Helper()
	operation := read[operations.Operation](d, method, path, input, status)
	if operation.ID != operationID {
		d.t.Fatal("missiontest: response does not identify the requested operation")
	}
	return operation
}

func (d *Driver) cleanup(operationID string) {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := "/api/v1/operations/" + operationID
	var operation operations.Operation
	err := d.call(ctx, http.MethodGet, path, nil, http.StatusOK, &operation)
	if hasStatus(err, http.StatusNotFound) {
		return
	}
	if err != nil || operation.ID != operationID {
		d.t.Errorf("missiontest: could not read owned operation %s during cleanup", operationID)
		return
	}
	if operation.Status.Terminal() {
		return
	}
	operation = operations.Operation{}
	err = d.call(ctx, http.MethodPost, path+"/cancel", nil, http.StatusOK, &operation)
	if err == nil && operation.ID == operationID && operation.Status.Terminal() {
		return
	}
	// Cancellation may have committed before an error. Read back, never resubmit.
	operation = operations.Operation{}
	if d.call(ctx, http.MethodGet, path, nil, http.StatusOK, &operation) == nil && operation.ID == operationID && operation.Status.Terminal() {
		return
	}
	d.t.Errorf("missiontest: cancellation of owned operation %s could not be confirmed", operationID)
}
