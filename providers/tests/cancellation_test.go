package providers_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"commander/framework/domain/operations"
	"commander/framework/id"
)

func TestOperationCancellationUsesLatestSnapshotAndPreservesEvidence(t *testing.T) {
	dir := t.TempDir()
	first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op, _ := createResultOperation(t, first)
	latest, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		for _, status := range []operations.TaskStatus{operations.TaskQueued, operations.TaskInterrupted, operations.TaskSucceeded, operations.TaskQueued} {
			task := next.Tasks[0]
			task.ID, task.Status = id.TaskID(), status
			task.CommandID = task.ID
			task.Continuation = &operations.Continuation{Version: 3, Status: operations.ContinuationPending, DecisionBy: operations.DecisionModel}
			next.Tasks = append(next.Tasks, task)
		}
		next.Questions = []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionPending, Text: "Keep this question"}}
		next.ReportID = "latest report"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()

	published, err := first.Cancel(t.Context(), op.ID)

	if err != nil {
		t.Fatal(err)
	}
	assertCancellation(t, latest, published, started)
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, published)
}

func TestOperationCancellationRejectsTerminalSnapshotsWithoutWriting(t *testing.T) {
	for _, status := range []operations.Status{operations.Completed, operations.Failed, operations.Cancelled} {
		t.Run(string(status), func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			op, _ := createQueuedDispatchOperation(t, first)
			var latest operations.Operation
			var err error
			if status == operations.Cancelled {
				latest, err = second.Cancel(t.Context(), op.ID)
			} else {
				latest, err = second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
					next.Status = status
					return nil
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "operations", op.ID+".json")
			before := readDispatchFile(t, path)

			published, err := first.Cancel(t.Context(), op.ID)

			if !errors.Is(err, operations.ErrFinished) || published.ID != "" {
				t.Fatalf("terminal cancellation must reject without a published snapshot: %+v, %v", published, err)
			}
			if !bytes.Equal(before, readDispatchFile(t, path)) {
				t.Fatal("rejected cancellation changed the operation file or storage revision")
			}
			saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertResultJSONEqual(t, saved, latest)
		})
	}
}

func TestOperationCancellationSerializesWithDispatchAndAutomaticChildren(t *testing.T) {
	for _, dispatch := range []bool{true, false} {
		name := "automatic children"
		if dispatch {
			name = "dispatch"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			var op operations.Operation
			var result operations.TaskResult
			var compete func() (operations.Operation, error)
			if dispatch {
				var request operations.TaskDispatch
				op, request = createQueuedDispatchOperation(t, first)
				compete = func() (operations.Operation, error) { return second.DispatchTask(t.Context(), op.ID, request) }
			} else {
				op, result = createResultOperation(t, first)
				compete = func() (operations.Operation, error) { return second.FinishTask(t.Context(), op.ID, result) }
			}
			type completion struct {
				op  operations.Operation
				err error
			}
			gate := make(chan struct{})
			cancelled, competing := make(chan completion, 1), make(chan completion, 1)
			go func() {
				<-gate
				published, err := first.Cancel(t.Context(), op.ID)
				cancelled <- completion{published, err}
			}()
			go func() {
				<-gate
				published, err := compete()
				competing <- completion{published, err}
			}()
			started := time.Now().UTC()
			close(gate)
			cancellation, work := <-cancelled, <-competing
			if cancellation.err != nil {
				t.Fatalf("cancellation failed: %v", cancellation.err)
			}
			if work.err != nil && (!dispatch || !errors.Is(work.err, operations.ErrOperationConflict)) {
				t.Fatalf("competing write failed unexpectedly: %v", work.err)
			}
			before := op
			if work.err == nil && work.op.Status != operations.Cancelled {
				before = work.op
			}
			assertCancellation(t, before, cancellation.op, started)
			saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Status != operations.Cancelled || countResultEvents(saved, operations.OperationCancelled) != 1 {
				t.Fatalf("competing write lost or duplicated cancellation: %+v", saved)
			}
			for _, task := range saved.Tasks {
				if task.Status == operations.TaskQueued || task.Status == operations.TaskCancelled && task.FinishedAt == nil {
					t.Fatalf("cancellation left queued work or an unfinished cancelled task: %+v", task)
				}
			}
			if dispatch {
				if work.err == nil {
					assertResultJSONEqual(t, saved.Tasks[0], work.op.Tasks[0])
					if saved.Tasks[0].Status != operations.TaskRunning || countResultEvents(saved, operations.TaskDispatched) != 1 {
						t.Fatalf("cancellation lost the winning dispatch: %+v", saved)
					}
				} else if saved.Tasks[0].Status != operations.TaskCancelled || countResultEvents(saved, operations.TaskDispatched) != 0 {
					t.Fatalf("rejected dispatch changed the cancelled assignment: %+v", saved)
				}
			} else {
				if saved.Tasks[0].Status != operations.TaskSucceeded || countResultEvents(saved, operations.TaskFinished) != 1 || len(saved.Tasks) != len(work.op.Tasks) {
					t.Fatalf("cancellation lost a result or automatic child transaction: %+v", saved)
				}
				assertResultJSONEqual(t, saved.Tasks[0], work.op.Tasks[0])
				assertResultJSONEqual(t, saved.Tasks[0].Result, result.Result)
				if work.op.Status == operations.Cancelled && len(saved.Tasks) != 1 {
					t.Fatalf("late result created children after cancellation: %+v", saved.Tasks)
				}
			}
		})
	}
}

func TestOperationCancellationWriteFailuresPreservePublicationState(t *testing.T) {
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
			op, _ := createQueuedDispatchOperation(t, store)
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

			published, err := store.Cancel(t.Context(), op.ID)
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
				assertCancellation(t, op, published, started)
				saved, err := reopened.Get(t.Context(), op.ID)
				if err != nil {
					t.Fatal(err)
				}
				assertResultJSONEqual(t, saved, published)
			} else if !bytes.Equal(before, after) || published.ID != "" {
				t.Fatal("failed write exposed or persisted an uncommitted cancellation")
			}
			retried, err := reopened.Cancel(t.Context(), op.ID)
			if uncertain {
				if !errors.Is(err, operations.ErrFinished) || retried.ID != "" || !bytes.Equal(after, readDispatchFile(t, path)) {
					t.Fatalf("retry changed the published cancellation or storage revision: %+v, %v", retried, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertCancellation(t, op, retried, started)
			}
		})
	}
}

func assertCancellation(t *testing.T, before, published operations.Operation, started time.Time) {
	t.Helper()
	if published.Status != operations.Cancelled || published.UpdatedAt.Before(started) || published.UpdatedAt.After(time.Now().UTC()) || len(published.Events) != len(before.Events)+1 || len(published.Tasks) != len(before.Tasks) {
		t.Fatalf("cancellation did not atomically publish status, timestamp, and one event: %+v", published)
	}
	event := published.Events[len(published.Events)-1]
	if event.Type != operations.OperationCancelled || event.OperationID != before.ID || event.Status != operations.Cancelled || event.TaskID != "" || event.QuestionID != "" || event.Sequence != uint64(len(before.Events)+1) || event.CreatedAt.Before(started) || event.CreatedAt.After(published.UpdatedAt) {
		t.Fatalf("cancellation event lost its identity, order, or commit time: %+v", event)
	}
	expected := before.Clone()
	expected.Status, expected.UpdatedAt = operations.Cancelled, published.UpdatedAt
	expected.Events = append(expected.Events, event)
	for i := range expected.Tasks {
		if expected.Tasks[i].Status == operations.TaskQueued {
			finished := published.Tasks[i].FinishedAt
			if finished == nil || finished.Before(started) || finished.After(event.CreatedAt) {
				t.Fatalf("queued task did not record its cancellation time: %+v", published.Tasks[i])
			}
			expected.Tasks[i].Status = operations.TaskCancelled
			expected.Tasks[i].FinishedAt = finished
		}
	}
	assertResultJSONEqual(t, published, expected)
}
