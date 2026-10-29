package fixtures

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

func CreateQueuedDispatchOperation(t *testing.T, store *operationfs.OperationFsStore) (operations.Operation, operations.TaskDispatch) {
	t.Helper()
	created := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	operationID, taskID := id.OperationID(), id.TaskID()
	op := operations.Operation{
		ID: operationID, Status: operations.Planning, CreatedAt: created, UpdatedAt: created,
		Preflight: &operations.Preflight{Ready: true, Version: 2},
		Tasks: []operations.Task{{
			ID: taskID, OperationID: operationID, ParentTaskID: id.TaskID(),
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

func ExpectedDispatch(op operations.Operation, dispatch operations.TaskDispatch, updatedAt time.Time) operations.Operation {
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

func NewDispatchStore(t *testing.T, dir string) *operationfs.OperationFsStore {
	t.Helper()
	store, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func ReadDispatchFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
