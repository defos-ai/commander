package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

func TestOperationDispatchHasOneOwnerAcrossStoreInstances(t *testing.T) {
	dir := t.TempDir()
	first := newDispatchStore(t, dir)
	second := newDispatchStore(t, dir)
	op, dispatch := createQueuedDispatchOperation(t, first)
	type result struct {
		dispatch  operations.TaskDispatch
		operation operations.Operation
		err       error
	}
	gate, results := make(chan struct{}), make(chan result, 2)

	for i, store := range []*operationfs.OperationFsStore{first, second} {
		request := dispatch
		request.AgentID = []string{"review-agent-00000001", "review-agent-00000002"}[i]
		go func() {
			<-gate
			published, err := store.DispatchTask(t.Context(), op.ID, request)
			results <- result{request, published, err}
		}()
	}
	started := time.Now().UTC()
	close(gate)
	a, b := <-results, <-results

	if a.err != nil {
		a, b = b, a
	}
	if a.err != nil || !errors.Is(b.err, operations.ErrOperationConflict) {
		t.Fatalf("dispatch must have one winner and one conflict: %v, %v", a.err, b.err)
	}
	if a.operation.UpdatedAt.Before(started) || a.operation.UpdatedAt.After(time.Now().UTC()) {
		t.Fatalf("dispatch did not record its commit time: %v", a.operation.UpdatedAt)
	}
	expected := expectedDispatch(op, a.dispatch, a.operation.UpdatedAt)
	if !reflect.DeepEqual(a.operation, expected) {
		t.Fatalf("dispatch changed the assignment or omitted transition fields:\n got %+v\nwant %+v", a.operation, expected)
	}
	reopened := newDispatchStore(t, dir)
	saved, err := reopened.Get(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(saved, expected) {
		t.Fatalf("reopening lost the winning dispatch or appended another event: %+v, %v", saved, err)
	}
	a.operation.Tasks[0].Input[0] = '9'
	*a.operation.Tasks[0].DeadlineAt = a.operation.Tasks[0].DeadlineAt.Add(time.Hour)
	a.operation.Events[len(a.operation.Events)-1].TaskID = id.TaskID()
	again, err := first.Get(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(again, expected) {
		t.Fatalf("mutating the dispatch result changed storage: %+v, %v", again, err)
	}
}

func TestOperationDispatchRejectsChangedStoredState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*operations.Operation)
	}{
		{"already claimed", func(op *operations.Operation) { op.Tasks[0].Status = operations.TaskRunning }},
		{"preflight blocked", func(op *operations.Operation) { op.Preflight.Ready = false }},
		{"operation cancelled", func(op *operations.Operation) { op.Status = operations.Cancelled }},
		{"agent already assigned", func(op *operations.Operation) { op.Tasks[0].AgentID = "review-agent-00000002" }},
		{"question pending", func(op *operations.Operation) {
			op.Questions = append(op.Questions, operations.Question{ID: id.QuestionID(), Status: operations.QuestionPending})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first := newDispatchStore(t, dir)
			second := newDispatchStore(t, dir)
			op, dispatch := createQueuedDispatchOperation(t, first)
			latest, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				tc.change(next)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "operations", op.ID+".json")
			before := readDispatchFile(t, path)

			_, err = first.DispatchTask(t.Context(), op.ID, dispatch)

			if !errors.Is(err, operations.ErrOperationConflict) {
				t.Fatalf("dispatch accepted changed eligibility: %v", err)
			}
			if after := readDispatchFile(t, path); !bytes.Equal(before, after) {
				t.Fatal("rejected dispatch changed the operation file or its revision")
			}
			saved, err := second.Get(t.Context(), op.ID)
			if err != nil || !reflect.DeepEqual(saved, latest) {
				t.Fatalf("rejected dispatch changed the latest snapshot: %+v, %v", saved, err)
			}
		})
	}
}

func TestOperationDispatchWriteFailureLeavesAssignmentQueued(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions and an unprivileged user")
	}
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op, dispatch := createQueuedDispatchOperation(t, store)
	operationDir := filepath.Join(dir, "operations")
	path := filepath.Join(operationDir, op.ID+".json")
	before := readDispatchFile(t, path)
	restore := func() {
		t.Helper()
		if err := os.Chmod(operationDir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(restore)
	if err := os.Chmod(operationDir, 0500); err != nil {
		t.Fatal(err)
	}

	_, err := store.DispatchTask(t.Context(), op.ID, dispatch)
	restore()

	if err == nil {
		t.Skip("directory write permissions are not enforced")
	}
	if !errors.Is(err, fs.ErrPermission) || errors.Is(err, operations.ErrWriteUncertain) {
		t.Fatalf("expected permission failure before publishing the operation: %v", err)
	}
	if after := readDispatchFile(t, path); !bytes.Equal(before, after) {
		t.Fatal("failed write partially dispatched the task or changed its revision")
	}
	retried, err := store.DispatchTask(t.Context(), op.ID, dispatch)
	if err != nil || !reflect.DeepEqual(retried, expectedDispatch(op, dispatch, retried.UpdatedAt)) {
		t.Fatalf("dispatch could not retry the unchanged assignment: %+v, %v", retried, err)
	}
}

func TestOperationDispatchDirectorySyncFailureLeavesAssignmentQueued(t *testing.T) {
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op, dispatch := createQueuedDispatchOperation(t, store)
	path := filepath.Join(dir, "operations", op.ID+".json")
	before := readDispatchFile(t, path)
	restore := denyDirectoryRead(t, filepath.Join(dir, "operations"))

	published, err := store.DispatchTask(t.Context(), op.ID, dispatch)
	restore()

	if errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("pre-publication sync failure lost its cause or reported uncertain durability: %v", err)
	}
	if published.ID != "" || !bytes.Equal(before, readDispatchFile(t, path)) {
		t.Fatal("failed sync exposed or persisted an uncommitted dispatch")
	}
	reopened := newDispatchStore(t, dir)
	saved, err := reopened.Get(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(saved, op) {
		t.Fatalf("failed sync changed the queued assignment: %+v, %v", saved, err)
	}
	retried, err := reopened.DispatchTask(t.Context(), op.ID, dispatch)
	if err != nil || !reflect.DeepEqual(retried, expectedDispatch(op, dispatch, retried.UpdatedAt)) {
		t.Fatalf("dispatch could not retry after directory permissions recovered: %+v, %v", retried, err)
	}
}

func createQueuedDispatchOperation(t *testing.T, store *operationfs.OperationFsStore) (operations.Operation, operations.TaskDispatch) {
	t.Helper()
	created := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	operationID, taskID := id.OperationID(), id.TaskID()
	op := operations.Operation{
		ID: operationID, Status: operations.Planning, CreatedAt: created, UpdatedAt: created,
		Preflight: &operations.Preflight{Ready: true, Version: 2},
		Tasks: []operations.Task{{
			ID: taskID, OperationID: operationID, ParentTaskID: id.TaskID(), StepID: "review",
			ActionName: "inspect", CommandID: taskID, Input: json.RawMessage(`{"page":1}`), Status: operations.TaskQueued,
		}},
		Events: []operations.Event{{Sequence: 1, OperationID: operationID, Type: operations.TaskAssigned,
			Status: operations.Planning, TaskID: taskID, CreatedAt: created}},
	}
	dispatch := operations.TaskDispatch{TaskID: taskID, AgentID: "review-agent-00000001",
		StartedAt: created.Add(time.Minute), DeadlineAt: created.Add(2 * time.Minute)}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved, dispatch
}

func expectedDispatch(op operations.Operation, dispatch operations.TaskDispatch, updatedAt time.Time) operations.Operation {
	op = op.Clone()
	task := &op.Tasks[0]
	task.Status, task.AgentID, task.StartedAt = operations.TaskRunning, dispatch.AgentID, dispatch.StartedAt
	task.DeadlineAt = &dispatch.DeadlineAt
	op.Status, op.UpdatedAt = operations.Running, updatedAt
	op.Events = append(op.Events, operations.Event{
		Sequence: 2, OperationID: op.ID, Type: operations.TaskDispatched, Status: operations.Running,
		TaskID: dispatch.TaskID, Message: "Task dispatched", CreatedAt: updatedAt,
	})
	return op
}

func newDispatchStore(t *testing.T, dir string) *operationfs.OperationFsStore {
	t.Helper()
	store, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func readDispatchFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
