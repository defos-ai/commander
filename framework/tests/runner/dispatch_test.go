package runner_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"commander/framework/domain/operations"
)

func TestDispatchWriteFailureKeepsTaskQueuedUntilRetry(t *testing.T) {
	config := fixture()
	store := &dispatchFailureStore{OperationStore: config.OperationStore, attempted: make(chan struct{}, 1)}
	config.OperationStore = store
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)

	task := assign(t, r, op.ID, "inspect")
	select {
	case <-store.attempted:
	case <-time.After(5 * time.Second):
		t.Fatal("expected dispatch reservation attempt")
	}
	// Reading runner events waits for the failed scheduler pass to finish.
	events, err := r.Events(t.Context(), op.ID, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	stored, err := r.Get(t.Context(), op.ID)
	if err != nil || len(stored.Tasks) != 1 || !reflect.DeepEqual(stored.Tasks[0], task) {
		t.Fatalf("failed reservation changed the queued task: %+v, %v", stored, err)
	}
	if len(events) == 0 || events[len(events)-1].Type != operations.TaskAssigned || !reflect.DeepEqual(stored.Events, events) {
		t.Fatalf("failed reservation changed dispatch events: stored=%+v, runner=%+v", stored.Events, events)
	}
	select {
	case call := <-calls:
		t.Fatalf("executed without a saved reservation: %+v", call)
	default:
	}

	store.allowed.Store(true)
	call := receive(t, calls)
	reserved, err := r.Get(t.Context(), op.ID)
	if err != nil || len(reserved.Tasks) != 1 || reserved.Tasks[0].Status != operations.TaskRunning || reserved.Tasks[0].CommandID != task.ID || reserved.Tasks[0].StartedAt.IsZero() || reserved.Tasks[0].DeadlineAt == nil {
		t.Fatalf("retry executed without a complete reservation: %+v, %v", reserved, err)
	}
	if call.agent != reserved.Tasks[0].AgentID || task.AgentID != "" || call.call.Work.TaskID != task.ID || len(reserved.Events) != len(events)+1 || reserved.Events[len(events)].Type != operations.TaskDispatched {
		t.Fatalf("retry changed or duplicated the dispatch: call=%+v, operation=%+v", call, reserved)
	}
	call.reply <- output("inspect")
	awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
}

type dispatchFailureStore struct {
	operations.OperationStore
	allowed   atomic.Bool
	attempted chan struct{}
}

func (s *dispatchFailureStore) DispatchTask(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
	if !s.allowed.Load() {
		select {
		case s.attempted <- struct{}{}:
		default:
		}
		return operations.Operation{}, errors.New("database unavailable")
	}
	return s.OperationStore.DispatchTask(ctx, operationID, dispatch)
}
