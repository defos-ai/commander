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

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestOperationQueuePreservesConcurrentAssignmentsAcrossStores(t *testing.T) {
	dir := t.TempDir()
	first, second := fixtures.NewDispatchStore(t, dir), fixtures.NewDispatchStore(t, dir)
	op, firstTask := fixtures.CreateQueueOperation(t, first)
	firstTask.Input = json.RawMessage(`{"page":1}`)
	secondTask := operations.NewTask(op.ID, "review", "", actions.NextAction{
		ActionName: "inspect", Input: json.RawMessage(`{"page":2}`),
	})
	requests := []operations.Task{firstTask, secondTask}
	type result struct {
		op  operations.Operation
		err error
	}
	gate, results := make(chan struct{}), make(chan result, 2)
	for i, store := range []*operationfs.OperationFsStore{first, second} {
		go func() {
			<-gate
			published, err := store.QueueTask(t.Context(), op.ID, requests[i])
			results <- result{published, err}
		}()
	}
	started := time.Now().UTC()
	close(gate)
	a, b := <-results, <-results
	if len(a.op.Tasks) > len(b.op.Tasks) {
		a, b = b, a
	}
	if a.err != nil || b.err != nil || len(a.op.Tasks) != 1 || len(b.op.Tasks) != 2 {
		t.Fatalf("concurrent assignments lost a task: first=%+v, %v; second=%+v, %v", a.op, a.err, b.op, b.err)
	}
	for _, published := range []operations.Operation{a.op, b.op} {
		if published.Status != operations.Running || published.UpdatedAt.Before(started) || published.UpdatedAt.After(time.Now().UTC()) || len(published.Events) != len(published.Tasks)+1 {
			t.Fatalf("queue omitted its status, commit time, or event: %+v", published)
		}
		for i, task := range published.Tasks {
			want := firstTask
			if task.ID == secondTask.ID {
				want = secondTask
			}
			assertResultJSONEqual(t, task, want)
			event := published.Events[i+1]
			if event.Sequence != uint64(i+2) || event.OperationID != op.ID || event.TaskID != task.ID || event.Type != operations.TaskAssigned || event.Status != operations.Running || event.CreatedAt.Before(started) || event.CreatedAt.After(time.Now().UTC()) {
				t.Fatalf("assignment event does not match its committed task: %+v", event)
			}
		}
	}
	if b.op.Tasks[0].ID == b.op.Tasks[1].ID {
		t.Fatal("concurrent assignments duplicated one task")
	}
	saved, err := fixtures.NewDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, b.op)
}

func TestOperationQueueReplayPreservesDispatchedAndTerminalState(t *testing.T) {
	dir := t.TempDir()
	store := fixtures.NewDispatchStore(t, dir)
	op, task := fixtures.CreateQueueOperation(t, store)
	task.Input = json.RawMessage(`{"page":9007199254740992,"flags":{"checked":true,"fresh":false}}`)
	if _, err := store.QueueTask(t.Context(), op.ID, task); err != nil {
		t.Fatal(err)
	}
	dispatch := operations.TaskDispatch{TaskID: task.ID, AgentID: "review-agent-00000001",
		StartedAt: time.Now().UTC(), DeadlineAt: time.Now().UTC().Add(time.Minute)}
	if _, err := store.DispatchTask(t.Context(), op.ID, dispatch); err != nil {
		t.Fatal(err)
	}
	replay := task
	replay.Input = json.RawMessage(`{ "flags": { "fresh": false, "checked": true }, "page": 9007199254740992 }`)
	pinned := replay
	pinned.AgentID = dispatch.AgentID
	wrongAgent := replay
	wrongAgent.AgentID = "review-agent-00000002"
	differentInput := replay
	differentInput.Input = json.RawMessage(`{"page":9007199254740993,"flags":{"checked":true,"fresh":false}}`)
	differentAction := replay
	differentAction.ActionName = "report"
	for _, terminal := range []bool{false, true} {
		name := "dispatched"
		if terminal {
			name = "terminal"
		}
		t.Run(name, func(t *testing.T) {
			if terminal {
				if _, err := store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
					next.Status = operations.Cancelled
					next.Tasks[0].Status = operations.TaskCancelled
					next.Tasks[0].FinishedAt = &dispatch.DeadlineAt
					next.Mission.Steps = nil
					next.Questions = []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionPending}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			reopened := fixtures.NewDispatchStore(t, dir)
			saved, err := reopened.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "operations", op.ID+".json")
			before := fixtures.ReadDispatchFile(t, path)
			for _, tc := range []struct {
				name     string
				task     operations.Task
				conflict bool
			}{
				{"equivalent JSON without an assigned agent", replay, false},
				{"preassigned matching agent", pinned, true},
				{"preassigned different agent", wrongAgent, true},
				{"different large integer", differentInput, true},
				{"different action", differentAction, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					published, err := reopened.QueueTask(t.Context(), op.ID, tc.task)
					if tc.conflict {
						if err == nil || tc.task.AgentID == "" && !errors.Is(err, operations.ErrOperationConflict) {
							t.Fatalf("conflicting assignment accepted: %v", err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						assertResultJSONEqual(t, published, saved)
					}
					if !bytes.Equal(before, fixtures.ReadDispatchFile(t, path)) {
						t.Fatal("assignment retry changed execution state, events, timestamp, or storage revision")
					}
				})
			}
		})
	}
}

func TestOperationQueueChecksLatestStateAndAllowsPreflightWaiting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*operations.Operation)
		blocked bool
		wantErr error
		status  operations.Status
	}{
		{"mission step removed", func(op *operations.Operation) { op.Mission.Steps = nil }, true, nil, ""},
		{"operation cancelled", func(op *operations.Operation) { op.Status = operations.Cancelled }, true, operations.ErrFinished, ""},
		{"global question", func(op *operations.Operation) {
			op.Questions = []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionPending}}
		}, true, operations.ErrQuestionPending, ""},
		{"matching step question", func(op *operations.Operation) {
			op.Questions = []operations.Question{{ID: id.QuestionID(), StepID: "review", Status: operations.QuestionPending}}
		}, true, operations.ErrQuestionPending, ""},
		{"unrelated step question", func(op *operations.Operation) {
			op.Questions = []operations.Question{{ID: id.QuestionID(), StepID: "report", Status: operations.QuestionPending}}
		}, false, nil, operations.Waiting},
		{"task linked question", func(op *operations.Operation) {
			op.Questions = []operations.Question{{ID: id.QuestionID(), TaskID: id.TaskID(), StepID: "review", Status: operations.QuestionPending}}
		}, false, nil, operations.Waiting},
		{"preflight not ready", func(op *operations.Operation) { op.Preflight.Ready = false }, false, nil, operations.Waiting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first, second := fixtures.NewDispatchStore(t, dir), fixtures.NewDispatchStore(t, dir)
			op, task := fixtures.CreateQueueOperation(t, first)
			latest, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				tc.change(next)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "operations", op.ID+".json")
			before := fixtures.ReadDispatchFile(t, path)

			published, err := first.QueueTask(t.Context(), op.ID, task)

			if tc.blocked {
				if err == nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("queue ignored latest assignment guard: %v", err)
				}
				if !bytes.Equal(before, fixtures.ReadDispatchFile(t, path)) {
					t.Fatal("rejected assignment changed the latest snapshot or revision")
				}
			} else {
				if err != nil || published.Status != tc.status || len(published.Tasks) != 1 || len(published.Events) != len(latest.Events)+1 {
					t.Fatalf("eligible assignment did not retain its waiting state: %+v, %v", published, err)
				}
				assertResultJSONEqual(t, published.Tasks[0], task)
				assertResultJSONEqual(t, published.Preflight, latest.Preflight)
				assertResultJSONEqual(t, published.Questions, latest.Questions)
				latest = published
			}
			saved, err := second.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertResultJSONEqual(t, saved, latest)
		})
	}
}

func TestOperationQueueWriteFailuresAreSafeToRetry(t *testing.T) {
	for _, permission := range []string{"write", "read"} {
		t.Run("denied "+permission, func(t *testing.T) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("requires Unix directory permissions and an unprivileged user")
			}
			dir := t.TempDir()
			store := fixtures.NewDispatchStore(t, dir)
			op, task := fixtures.CreateQueueOperation(t, store)
			operationDir := filepath.Join(dir, "operations")
			path := filepath.Join(operationDir, op.ID+".json")
			before := fixtures.ReadDispatchFile(t, path)
			restore := func() {
				t.Helper()
				if err := os.Chmod(operationDir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if permission == "read" {
				restore = fixtures.DenyDirectoryRead(t, operationDir)
			} else {
				t.Cleanup(restore)
				if err := os.Chmod(operationDir, 0500); err != nil {
					t.Fatal(err)
				}
			}

			published, err := store.QueueTask(t.Context(), op.ID, task)
			restore()

			if err == nil && permission == "write" {
				t.Skip("directory permissions are not enforced")
			}
			if !errors.Is(err, fs.ErrPermission) || errors.Is(err, operations.ErrWriteUncertain) {
				t.Fatalf("pre-publication failure lost its cause or reported uncertain durability: %v", err)
			}
			if !bytes.Equal(before, fixtures.ReadDispatchFile(t, path)) || published.ID != "" {
				t.Fatal("failed write exposed or persisted an uncommitted mutation")
			}
			reopened := fixtures.NewDispatchStore(t, dir)
			saved, err := reopened.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertResultJSONEqual(t, saved, op)
			retried, err := reopened.QueueTask(t.Context(), op.ID, task)
			if err != nil || len(retried.Tasks) != 1 || retried.Status != operations.Running || countResultEvents(retried, operations.TaskAssigned) != 1 {
				t.Fatalf("retry lost or duplicated the assignment: %+v, %v", retried, err)
			}
			assertResultJSONEqual(t, retried.Tasks[0], task)
		})
	}
}
