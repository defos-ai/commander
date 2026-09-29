package runner_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/actions"
	"commander/framework/domain/operations"
)

func TestAssignmentWriteFailurePreservesRetryableState(t *testing.T) {
	config := fixture()
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	failure := errors.New("database unavailable")
	config.OperationStore.(*operationStore).failNextUpdate(failure)
	command := commands.AssignTaskCommand{OperationID: op.ID, AgentID: agentID, ActionName: "inspect"}

	_, assignErr := r.Assign(t.Context(), command)
	stored, err := r.Get(t.Context(), op.ID)
	if err != nil || !errors.Is(assignErr, failure) || !reflect.DeepEqual(stored, op) {
		t.Fatalf("failed assignment changed durable state: operation=%+v, assign=%v, get=%v", stored, assignErr, err)
	}
	events, err := r.Events(t.Context(), op.ID, 0, false)
	if err != nil || !reflect.DeepEqual(events, op.Events) {
		t.Fatalf("failed assignment changed runner events: %+v, %v", events, err)
	}
	select {
	case call := <-calls:
		t.Fatalf("uncommitted assignment dispatched: %+v", call)
	default:
	}

	task, err := r.Assign(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	call := receive(t, calls)
	call.reply <- output("inspect")
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if len(settled.Tasks) != 1 || settled.Tasks[0].ID != task.ID || call.call.Work.TaskID != task.ID {
		t.Fatalf("retry lost or duplicated the assignment: %+v", settled)
	}
}

func TestAssignmentReturnsRequestedTaskFromStoreSnapshot(t *testing.T) {
	config := fixture()
	config.OperationStore = &additionalQueuedTaskStore{OperationStore: config.OperationStore}
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)

	task := assign(t, r, op.ID, "inspect")
	stored, err := r.Get(t.Context(), op.ID)
	if err != nil || len(stored.Tasks) != 2 || task.ID != stored.Tasks[0].ID || task.CommandID != stored.Tasks[0].CommandID || task.ActionName != "inspect" {
		t.Fatalf("assignment returned a different queued task: task=%+v, operation=%+v, error=%v", task, stored, err)
	}
	if call := receive(t, calls); call.call.Work.TaskID != task.ID {
		t.Fatalf("dispatched a different assignment: %+v", call)
	}
}

// A store snapshot may contain another assignment committed after the requested task.
type additionalQueuedTaskStore struct{ operations.OperationStore }

func (s *additionalQueuedTaskStore) QueueTask(ctx context.Context, operationID string, task operations.Task) (operations.Operation, error) {
	op, err := s.OperationStore.QueueTask(ctx, operationID, task)
	if err != nil {
		return op, err
	}
	later := operations.NewTask(operationID, "", "", actions.NextAction{AgentID: agentID, ActionName: "follow-up"})
	return s.OperationStore.QueueTask(ctx, operationID, later)
}
