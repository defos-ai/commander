package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	operationfs "commander/providers/fs/controller/operations"
)

func TestOperationContinuationHasOneDecisionAcrossStoreInstances(t *testing.T) {
	dir := t.TempDir()
	first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op, selected := createContinuationOperation(t, first)
	type resolution struct {
		next []actions.NextAction
		op   operations.Operation
		err  error
	}
	gate, results := make(chan struct{}), make(chan resolution, 2)
	for i, store := range []*operationfs.OperationFsStore{first, second} {
		next := selected[:i+1]
		go func() {
			<-gate
			published, err := store.ResolveContinuation(t.Context(), op.ID, op.Tasks[0].ID, op.Tasks[0].Continuation.Version, next)
			results <- resolution{next, published, err}
		}()
	}
	started := time.Now().UTC()
	close(gate)
	a, b := <-results, <-results
	if a.err != nil {
		a, b = b, a
	}
	if a.err != nil || b.err == nil || b.op.ID != "" {
		t.Fatalf("continuation must publish one decision and reject the other: %v, %v", a.err, b.err)
	}
	assertContinuationResolution(t, op, a.op, a.next, started)
	reopened := newDispatchStore(t, dir)
	saved, err := reopened.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, a.op)
	path := filepath.Join(dir, "operations", op.ID+".json")
	before := readDispatchFile(t, path)
	if _, err := reopened.ResolveContinuation(t.Context(), op.ID, op.Tasks[0].ID, op.Tasks[0].Continuation.Version, a.next); err == nil {
		t.Fatal("replaying the winning decision at the same version must fail")
	}
	if !bytes.Equal(before, readDispatchFile(t, path)) {
		t.Fatal("replay changed the decision, child IDs, events, or storage revision")
	}
}

func TestOperationContinuationRejectsChangedStoredState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*operations.Operation)
		want   error
	}{
		{"task question", func(op *operations.Operation) {
			op.Questions = []operations.Question{{ID: id.QuestionID(), TaskID: op.Tasks[0].ID, Status: operations.QuestionPending}}
		}, operations.ErrQuestionPending},
		{"step question", func(op *operations.Operation) {
			op.Questions = []operations.Question{{ID: id.QuestionID(), StepID: op.Tasks[0].StepID, Status: operations.QuestionPending}}
		}, operations.ErrQuestionPending},
		{"global question", func(op *operations.Operation) {
			op.Questions = []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionPending}}
		}, operations.ErrQuestionPending},
		{"cancelled", func(op *operations.Operation) { op.Status = operations.Cancelled }, operations.ErrFinished},
		{"stale version", func(op *operations.Operation) { op.Tasks[0].Continuation.Version++ }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			op, selected := createContinuationOperation(t, first)
			latest, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				tc.change(next)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "operations", op.ID+".json")
			before := readDispatchFile(t, path)

			published, err := first.ResolveContinuation(t.Context(), op.ID, op.Tasks[0].ID, op.Tasks[0].Continuation.Version, selected)

			if err == nil || tc.want != nil && !errors.Is(err, tc.want) || published.ID != "" {
				t.Fatalf("resolution accepted changed eligibility or returned an uncommitted snapshot: %+v, %v", published, err)
			}
			if !bytes.Equal(before, readDispatchFile(t, path)) {
				t.Fatal("rejected resolution changed the latest operation or its storage revision")
			}
			saved, err := first.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertResultJSONEqual(t, saved, latest)
		})
	}
}

func TestOperationContinuationEmptyDecisionIsDurable(t *testing.T) {
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op, _ := createContinuationOperation(t, store)
	started := time.Now().UTC()
	selected := []actions.NextAction{}

	published, err := store.ResolveContinuation(t.Context(), op.ID, op.Tasks[0].ID, op.Tasks[0].Continuation.Version, selected)

	if err != nil {
		t.Fatal(err)
	}
	assertContinuationResolution(t, op, published, selected, started)
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, published)
	if saved.Tasks[0].Continuation.ProposedActions == nil || saved.Tasks[0].Continuation.CreatedTaskIDs == nil {
		t.Fatal("empty decision must retain explicit empty action and child lists")
	}
}

func TestOperationContinuationWriteFailuresPreserveAtomicDecision(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		name := "before publish"
		if uncertain {
			name = "after publish"
		}
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("requires Unix directory permissions and an unprivileged user")
			}
			dir := t.TempDir()
			store := newDispatchStore(t, dir)
			op, selected := createContinuationOperation(t, store)
			operationDir := filepath.Join(dir, "operations")
			path := filepath.Join(operationDir, op.ID+".json")
			before := readDispatchFile(t, path)
			restore := func() {
				t.Helper()
				if err := os.Chmod(operationDir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if uncertain {
				restore = denyDirectoryRead(t, operationDir)
			} else {
				t.Cleanup(restore)
				if err := os.Chmod(operationDir, 0500); err != nil {
					t.Fatal(err)
				}
			}
			started := time.Now().UTC()

			published, err := store.ResolveContinuation(t.Context(), op.ID, op.Tasks[0].ID, op.Tasks[0].Continuation.Version, selected)
			restore()

			if err == nil && !uncertain {
				t.Skip("directory permissions are not enforced")
			}
			if !errors.Is(err, fs.ErrPermission) || errors.Is(err, operations.ErrWriteUncertain) != uncertain {
				t.Fatalf("write failure lost its cause or publication state: %v", err)
			}
			after := readDispatchFile(t, path)
			reopened := newDispatchStore(t, dir)
			if uncertain {
				assertContinuationResolution(t, op, published, selected, started)
				saved, err := reopened.Get(t.Context(), op.ID)
				if err != nil {
					t.Fatal(err)
				}
				assertResultJSONEqual(t, saved, published)
			} else {
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) || !strings.HasPrefix(pathErr.Path, filepath.Join(operationDir, "."+op.ID+".json-")) {
					t.Fatalf("expected failure creating the temporary snapshot: %v", err)
				}
				if !bytes.Equal(before, after) || published.ID != "" {
					t.Fatal("failed write exposed or persisted an uncommitted decision")
				}
			}
			retried, err := reopened.ResolveContinuation(t.Context(), op.ID, op.Tasks[0].ID, op.Tasks[0].Continuation.Version, selected)
			if uncertain {
				if err == nil || !bytes.Equal(after, readDispatchFile(t, path)) {
					t.Fatal("retrying a published decision must fail without changing bytes or revision")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertContinuationResolution(t, op, retried, selected, started)
			}
		})
	}
}

func createContinuationOperation(t *testing.T, store *operationfs.OperationFsStore) (operations.Operation, []actions.NextAction) {
	t.Helper()
	created := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	operationID, taskID := id.OperationID(), id.TaskID()
	op := operations.Operation{
		ID: operationID, Status: operations.Waiting, CreatedAt: created, UpdatedAt: created,
		Tasks: []operations.Task{{ID: taskID, OperationID: operationID, CommandID: taskID,
			ParentTaskID: id.TaskID(), StepID: "review", ActionName: "discover", Status: operations.TaskSucceeded,
			Continuation: &operations.Continuation{Version: 7, DecisionBy: operations.DecisionModel,
				Status: operations.ContinuationPending, CreatedTaskIDs: []string{},
				ProposedActions: []actions.NextAction{{ActionName: "inspect", Input: json.RawMessage(`{"page":0}`)}},
			},
		}},
		Events: []operations.Event{{Sequence: 1, OperationID: operationID, TaskID: taskID,
			Type: operations.ContinuationReady, Status: operations.Waiting, CreatedAt: created}},
	}
	selected := []actions.NextAction{
		{ActionName: "inspect", AgentID: "review-agent-00000001", Input: json.RawMessage(`{"page":1}`), Reason: "Inspect first page"},
		{ActionName: "report", Input: json.RawMessage(`{"page":2}`), Reason: "Report second page"},
	}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op, selected
}

func assertContinuationResolution(t *testing.T, before, published operations.Operation, selected []actions.NextAction, started time.Time) {
	t.Helper()
	if len(published.Tasks) != len(before.Tasks)+len(selected) || len(published.Events) != len(before.Events)+len(selected)+1 {
		t.Fatalf("decision lost or duplicated children or events: %+v", published)
	}
	c := published.Tasks[0].Continuation
	if c == nil || c.Version != before.Tasks[0].Continuation.Version+1 || c.Status != operations.ContinuationResolved || c.DecisionBy != operations.DecisionModel || len(c.CreatedTaskIDs) != len(selected) {
		t.Fatalf("decision state was not committed with its children: %+v", c)
	}
	assertResultJSONEqual(t, c.ProposedActions, selected)
	seen := map[string]bool{before.Tasks[0].ID: true}
	for i, child := range published.Tasks[len(before.Tasks):] {
		if child.ID == "" || seen[child.ID] || child.ID != c.CreatedTaskIDs[i] || child.CommandID != child.ID ||
			child.OperationID != before.ID || child.ParentTaskID != before.Tasks[0].ID || child.StepID != before.Tasks[0].StepID ||
			child.ActionName != selected[i].ActionName || child.AgentID != selected[i].AgentID || child.Status != operations.TaskQueued {
			t.Fatalf("decision child lost its identity or assignment: %+v", child)
		}
		seen[child.ID] = true
		assertResultJSONEqual(t, child.Input, selected[i].Input)
		assigned := published.Events[len(before.Events)+i]
		if assigned.Type != operations.TaskAssigned || assigned.TaskID != child.ID {
			t.Fatalf("child and assignment event were not committed together: %+v", assigned)
		}
	}
	wantStatus := operations.Running
	if len(selected) == 0 {
		wantStatus = operations.Planning
	}
	if published.Status != wantStatus || published.UpdatedAt.Before(started) || published.UpdatedAt.After(time.Now().UTC()) {
		t.Fatalf("decision did not refresh operation status and timestamp: %+v", published)
	}
	event := published.Events[len(published.Events)-1]
	if countResultEvents(published, operations.ContinuationResolvedEvent) != 1 || event.Type != operations.ContinuationResolvedEvent || event.TaskID != before.Tasks[0].ID || event.Status != wantStatus {
		t.Fatalf("decision must have one resolution event for its parent: %+v", published.Events)
	}
	for i, event := range published.Events {
		if event.Sequence != uint64(i+1) || event.OperationID != before.ID {
			t.Fatalf("decision lost operation event ordering: %+v", published.Events)
		}
	}
}
