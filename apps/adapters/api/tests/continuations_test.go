package api_test

import (
	"net/http"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

func TestContinuationCreatesOneRecordedBranch(t *testing.T) {
	a := connect(t)
	op := a.start(fixtureText)
	a.task(op.ID, "write-summary", summaryInput())
	ready := a.wait(op.ID, func(op models.OperationResponse) bool { return modelDecision(op) != nil })
	root := *modelDecision(ready)
	if root.Status != "succeeded" {
		t.Fatalf("summary action failed: %+v", root)
	}
	path := operationPath(op.ID) + "/tasks/" + root.ID + "/continuation/resolve"
	for _, body := range []object{{"version": root.Continuation.Version}, {"version": root.Continuation.Version, "actions": nil}, {"version": root.Continuation.Version, "actions": []object{{"action_name": "write-summary", "agent_id": "pinned"}}}} {
		assertError(t, a.request(http.MethodPost, path, body), http.StatusBadRequest, "invalid_request")
	}
	actions := []object{{"action_name": "write-summary", "input": summaryInput(), "reason": "Save the reviewed result"}, {"action_name": "write-summary", "input": summaryInput()}}
	resolved := a.resolve(op.ID, root, actions)
	if len(resolved.Tasks) != 3 || resolved.Tasks[0].Continuation.Status != "resolved" || len(resolved.Tasks[0].Continuation.CreatedTaskIDs) != 2 {
		t.Fatalf("resolution failed to persist one branch: %+v", resolved)
	}
	assertError(t, a.request(http.MethodPost, path, object{"version": root.Continuation.Version, "actions": actions}), http.StatusConflict, "continuation_conflict")
	finished := a.wait(op.ID, tasksSettled)
	if len(finished.Tasks) != 3 {
		t.Fatalf("stale resolution duplicated children: %+v", finished.Tasks)
	}
	for _, child := range finished.Tasks[1:] {
		if child.ParentTaskID != root.ID || child.Status != "succeeded" || child.AgentID == "" || child.Result == nil {
			t.Fatalf("child lost ownership/result: %+v", child)
		}
	}
	completed := a.complete(op.ID, "Reviewed continuation branch")
	for _, task := range completed.Tasks {
		if task.Continuation != nil && task.Continuation.Status != "resolved" {
			t.Fatalf("completion left a pending model decision: %+v", task)
		}
	}
}
