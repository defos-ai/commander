package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/actions"
)

func TestContinuationCreatesOneRecordedBranch(t *testing.T) {
	a := fixtures.Connect(t)
	driver := a.MissionDriver()
	input := fixtures.StoryBatch(fixtures.FixtureTitle)
	rawInput, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	op := a.Start(fixtures.FixtureTitle)
	assigned := a.Request(http.MethodPost, fixtures.OperationPath(op.ID)+"/tasks", fixtures.Object{
		"action_name": "write-hn-digest", "input": input,
	})
	task := fixtures.Decode[models.TaskResponse](t, assigned, http.StatusCreated)
	if assigned.Header.Get("Location") != fixtures.OperationPath(op.ID)+"/tasks/"+task.ID || task.OperationID != op.ID || task.CommandID == "" {
		t.Fatalf("assignment lost task identity or location: %+v", task)
	}
	root := driver.WaitForContinuation(op.ID, task.ID)
	if root.Status != "succeeded" {
		t.Fatalf("digest action failed: %+v", root)
	}
	path := fixtures.OperationPath(op.ID) + "/tasks/" + root.ID + "/continuation/resolve"
	for _, body := range []fixtures.Object{{"version": root.Continuation.Version}, {"version": root.Continuation.Version, "actions": nil}, {"version": root.Continuation.Version, "actions": []fixtures.Object{{"action_name": "write-hn-digest", "agent_id": "pinned"}}}} {
		fixtures.AssertError(t, a.Request(http.MethodPost, path, body), http.StatusBadRequest, "invalid_request")
	}
	next := []actions.NextAction{
		{ActionName: "write-hn-digest", Input: rawInput, Reason: "Save the reviewed result"},
		{ActionName: "write-hn-digest", Input: rawInput},
	}
	resolved := driver.Resolve(op.ID, root, next...)
	if len(resolved.Tasks) != 3 || resolved.Tasks[0].Continuation.Status != "resolved" || len(resolved.Tasks[0].Continuation.CreatedTaskIDs) != 2 {
		t.Fatalf("resolution failed to persist one branch: %+v", resolved)
	}
	fixtures.AssertError(t, a.Request(http.MethodPost, path, fixtures.Object{"version": root.Continuation.Version, "actions": next}), http.StatusConflict, "continuation_conflict")
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
	if completed.Status != "completed" || !completed.Progress.Settled ||
		completed.Progress.FinishedTasks != len(completed.Tasks) || completed.Progress.TotalTasks != len(completed.Tasks) {
		t.Fatalf("completion response has incorrect progress: %+v", completed.Progress)
	}
	for _, task := range completed.Tasks {
		if task.Continuation != nil && task.Continuation.Status != "resolved" {
			t.Fatalf("completion left a pending model decision: %+v", task)
		}
	}
}
