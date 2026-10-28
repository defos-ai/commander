package api_test

import (
	"net/http"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
)

func TestContinuationCreatesOneRecordedBranch(t *testing.T) {
	a := fixtures.Connect(t)
	op := a.Start(fixtures.FixtureText)
	a.Task(op.ID, "write-summary", fixtures.SummaryInput())
	ready := a.Wait(op.ID, func(op models.OperationResponse) bool { return fixtures.ModelDecision(op) != nil })
	root := *fixtures.ModelDecision(ready)
	if root.Status != "succeeded" {
		t.Fatalf("summary action failed: %+v", root)
	}
	path := fixtures.OperationPath(op.ID) + "/tasks/" + root.ID + "/continuation/resolve"
	for _, body := range []fixtures.Object{{"version": root.Continuation.Version}, {"version": root.Continuation.Version, "actions": nil}, {"version": root.Continuation.Version, "actions": []fixtures.Object{{"action_name": "write-summary", "agent_id": "pinned"}}}} {
		fixtures.AssertError(t, a.Request(http.MethodPost, path, body), http.StatusBadRequest, "invalid_request")
	}
	actions := []fixtures.Object{{"action_name": "write-summary", "input": fixtures.SummaryInput(), "reason": "Save the reviewed result"}, {"action_name": "write-summary", "input": fixtures.SummaryInput()}}
	resolved := a.Resolve(op.ID, root, actions)
	if len(resolved.Tasks) != 3 || resolved.Tasks[0].Continuation.Status != "resolved" || len(resolved.Tasks[0].Continuation.CreatedTaskIDs) != 2 {
		t.Fatalf("resolution failed to persist one branch: %+v", resolved)
	}
	fixtures.AssertError(t, a.Request(http.MethodPost, path, fixtures.Object{"version": root.Continuation.Version, "actions": actions}), http.StatusConflict, "continuation_conflict")
	finished := a.Wait(op.ID, fixtures.TasksSettled)
	if len(finished.Tasks) != 3 {
		t.Fatalf("stale resolution duplicated children: %+v", finished.Tasks)
	}
	for _, child := range finished.Tasks[1:] {
		if child.ParentTaskID != root.ID || child.Status != "succeeded" || child.AgentID == "" || child.Result == nil {
			t.Fatalf("child lost ownership/result: %+v", child)
		}
	}
	completed := a.Complete(op.ID, "Reviewed continuation branch")
	for _, task := range completed.Tasks {
		if task.Continuation != nil && task.Continuation.Status != "resolved" {
			t.Fatalf("completion left a pending model decision: %+v", task)
		}
	}
}
