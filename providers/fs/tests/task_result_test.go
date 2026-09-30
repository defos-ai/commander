package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	operationfs "commander/providers/fs/controller/operations"
)

func TestOperationResultsSettleConcurrentChildrenAtomically(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "model decision"
		if automatic {
			name = "automatic children"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			op, parentResult := createResultOperation(t, first)
			if automatic {
				var err error
				op, err = first.Update(t.Context(), op.ID, func(next *operations.Operation) error {
					next.Mission.Automations["inspect"] = missions.Automation{Run: []string{"report"}, Continue: missions.WhenEach}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			op, err := first.FinishTask(t.Context(), op.ID, parentResult)
			if err != nil || len(op.Tasks) != 3 {
				t.Fatalf("parent did not publish both children: %+v, %v", op, err)
			}
			for _, child := range op.Tasks[1:] {
				op, err = first.DispatchTask(t.Context(), op.ID, operations.TaskDispatch{
					TaskID: child.ID, AgentID: child.AgentID,
					StartedAt: parentResult.FinishedAt.Add(time.Minute), DeadlineAt: parentResult.FinishedAt.Add(time.Hour),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			requests := []operations.TaskResult{
				{TaskID: op.Tasks[1].ID, AgentID: op.Tasks[1].AgentID, Status: operations.TaskSucceeded,
					Result: &actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{"page":1}`)}, FinishedAt: parentResult.FinishedAt.Add(2 * time.Minute)},
				{TaskID: op.Tasks[2].ID, AgentID: op.Tasks[2].AgentID, Status: operations.TaskSucceeded,
					Result: &actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{"page":2}`)}, FinishedAt: parentResult.FinishedAt.Add(2 * time.Minute)},
			}
			for i := range requests {
				requests[i].Result.NextActions = []actions.NextAction{{ActionName: "report", Input: requests[i].Result.Output}}
			}
			type completion struct {
				op  operations.Operation
				err error
			}
			gate, results := make(chan struct{}), make(chan completion, 2)

			for i, store := range []*operationfs.OperationFsStore{first, second} {
				go func() {
					<-gate
					saved, err := store.FinishTask(t.Context(), op.ID, requests[i])
					results <- completion{saved, err}
				}()
			}
			close(gate)
			a, b := <-results, <-results

			for _, result := range []completion{a, b} {
				if result.err != nil {
					t.Fatalf("concurrent completion failed: %v", result.err)
				}
				finished := 0
				for _, child := range result.op.Tasks[1:3] {
					if child.Status == operations.TaskSucceeded {
						finished++
					}
				}
				if (finished == 1) != (result.op.Tasks[0].Continuation.Status == operations.ContinuationWaiting) {
					t.Fatalf("group decision was not committed with the last child: %+v", result.op)
				}
			}
			saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			for i, request := range requests {
				child := saved.Tasks[i+1]
				if child.Status != operations.TaskSucceeded || child.Result == nil || child.Continuation == nil || child.Continuation.Status != operations.ContinuationResolved {
					t.Fatalf("group lost a completed child: %+v", child)
				}
				assertResultJSONEqual(t, child.Result, request.Result)
			}
			decision, wantTasks := operations.ContinuationReady, 3
			c := saved.Tasks[0].Continuation
			if automatic {
				decision, wantTasks = operations.ContinuationResolvedEvent, 5
				if c.DecisionBy != operations.DecisionCode || c.Status != operations.ContinuationResolved {
					t.Fatalf("automatic group was not resolved: %+v", c)
				}
			} else if c.DecisionBy != operations.DecisionModel || c.Status != operations.ContinuationPending || len(c.ProposedActions) != 2 {
				t.Fatalf("group must expose one decision containing both proposals: %+v", c)
			}
			decisions := countResultEvents(saved, operations.ContinuationReady) + countResultEvents(saved, operations.ContinuationResolvedEvent)
			if len(saved.Tasks) != wantTasks || countResultEvents(saved, operations.TaskFinished) != 3 || countResultEvents(saved, decision) != 1 || decisions != 1 {
				t.Fatalf("group duplicated or lost children or events: %+v", saved)
			}
			if automatic {
				for i, child := range saved.Tasks[3:] {
					if child.ParentTaskID != requests[i].TaskID || child.Status != operations.TaskQueued || child.ActionName != "report" {
						t.Fatalf("automatic child lost its assignment: %+v", child)
					}
					assertResultJSONEqual(t, child.Input, requests[i].Result.Output)
				}
			}
		})
	}
}

func TestOperationResultReplayPreservesPublishedChildrenAndBytes(t *testing.T) {
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op, request := createResultOperation(t, store)
	if _, err := store.FinishTask(t.Context(), op.ID, request); err != nil {
		t.Fatal(err)
	}
	reopened := newDispatchStore(t, dir)
	saved, err := reopened.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "operations", op.ID+".json")
	before := readDispatchFile(t, path)
	replay := request
	replay.FinishedAt = replay.FinishedAt.Add(time.Hour)
	late := operations.TaskResult{TaskID: request.TaskID, AgentID: request.AgentID,
		Status: operations.TaskInterrupted, Error: "observation timed out", FinishedAt: replay.FinishedAt}
	conflict := request
	conflict.Result = &actions.ActionOutput{ActionName: "discover", Output: json.RawMessage(`{"different":true}`)}
	wrongAgent := request
	wrongAgent.AgentID = "review-agent-00000002"

	for _, tc := range []struct {
		name     string
		request  operations.TaskResult
		conflict bool
	}{{"same outcome", replay, false}, {"late interruption", late, false}, {"different outcome", conflict, true}, {"different owner", wrongAgent, true}} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := reopened.FinishTask(t.Context(), op.ID, tc.request)

			if tc.conflict {
				if !errors.Is(err, operations.ErrOperationConflict) {
					t.Fatalf("conflicting completion accepted: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertResultJSONEqual(t, result, saved)
			}
			if !bytes.Equal(before, readDispatchFile(t, path)) {
				t.Fatal("replay changed the result, receipt time, children, events, or storage revision")
			}
		})
	}
}

func TestOperationResultAfterCancellationRetainsEvidenceWithoutChildren(t *testing.T) {
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op, request := createResultOperation(t, store)
	cancelled, err := newDispatchStore(t, dir).Cancel(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}

	published, err := store.FinishTask(t.Context(), op.ID, request)

	if err != nil || published.Status != operations.Cancelled || len(published.Tasks) != 1 || published.Tasks[0].Status != operations.TaskSucceeded {
		t.Fatalf("late result changed cancellation or lost evidence: %+v, %v", published, err)
	}
	assertResultJSONEqual(t, published.Tasks[0].Result, request.Result)
	if len(published.Events) != len(cancelled.Events)+1 || published.Events[len(published.Events)-1].Type != operations.TaskFinished {
		t.Fatalf("late result must publish only its completion event: %+v", published.Events)
	}
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, published)
}

func TestOperationResultWriteFailuresAreSafeToRetry(t *testing.T) {
	for _, permission := range []string{"write", "read"} {
		t.Run("denied "+permission, func(t *testing.T) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("requires Unix directory permissions and an unprivileged user")
			}
			dir := t.TempDir()
			store := newDispatchStore(t, dir)
			op, request := createResultOperation(t, store)
			operationDir := filepath.Join(dir, "operations")
			path := filepath.Join(operationDir, op.ID+".json")
			before := readDispatchFile(t, path)
			restore := func() {
				t.Helper()
				if err := os.Chmod(operationDir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if permission == "read" {
				restore = denyDirectoryRead(t, operationDir)
			} else {
				t.Cleanup(restore)
				if err := os.Chmod(operationDir, 0500); err != nil {
					t.Fatal(err)
				}
			}

			published, err := store.FinishTask(t.Context(), op.ID, request)
			restore()

			if err == nil && permission == "write" {
				t.Skip("directory permissions are not enforced")
			}
			if !errors.Is(err, fs.ErrPermission) || errors.Is(err, operations.ErrWriteUncertain) {
				t.Fatalf("pre-publication failure lost its cause or reported uncertain durability: %v", err)
			}
			if !bytes.Equal(before, readDispatchFile(t, path)) || published.ID != "" {
				t.Fatal("failed write exposed or persisted an uncommitted mutation")
			}
			reopened := newDispatchStore(t, dir)
			saved, err := reopened.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertResultJSONEqual(t, saved, op.Clone())
			request.FinishedAt = request.FinishedAt.Add(time.Hour)
			retried, err := reopened.FinishTask(t.Context(), op.ID, request)
			if err != nil || len(retried.Tasks) != 3 || retried.Tasks[0].Status != operations.TaskSucceeded || countResultEvents(retried, operations.TaskAssigned) != 2 || countResultEvents(retried, operations.TaskFinished) != 1 {
				t.Fatalf("retry lost or duplicated the completion transaction: %+v, %v", retried, err)
			}
		})
	}
}

func createResultOperation(t *testing.T, store *operationfs.OperationFsStore) (operations.Operation, operations.TaskResult) {
	t.Helper()
	created := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	operationID, taskID := id.OperationID(), id.TaskID()
	op := operations.Operation{
		ID: operationID, Status: operations.Running, CreatedAt: created, UpdatedAt: created,
		Mission: missions.Mission{Automations: map[string]missions.Automation{
			"discover": {Run: []string{"inspect"}, Continue: missions.WhenAll},
		}},
		Preflight: &operations.Preflight{Ready: true, Version: 2},
		Tasks: []operations.Task{{ID: taskID, OperationID: operationID, CommandID: taskID,
			AgentID: "review-agent-00000001", ActionName: "discover", StepID: "review",
			Status: operations.TaskRunning, StartedAt: created}},
		Events: []operations.Event{{Sequence: 1, OperationID: operationID, TaskID: taskID,
			Type: operations.TaskDispatched, Status: operations.Running, CreatedAt: created}},
	}
	request := operations.TaskResult{TaskID: taskID, AgentID: op.Tasks[0].AgentID,
		Status: operations.TaskSucceeded, FinishedAt: created.Add(time.Minute),
		Result: &actions.ActionOutput{ActionName: "discover", Output: json.RawMessage(`{"found":2}`),
			NextActions: []actions.NextAction{
				{ActionName: "inspect", AgentID: "review-agent-00000001", Input: json.RawMessage(`{"page":1}`)},
				{ActionName: "inspect", AgentID: "review-agent-00000002", Input: json.RawMessage(`{"page":2}`)},
			}},
	}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op, request
}

func countResultEvents(op operations.Operation, kind operations.EventType) int {
	count := 0
	for _, event := range op.Events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

func assertResultJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	a, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("result differs:\n got %s\nwant %s", a, b)
	}
}
