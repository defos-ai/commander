package runner_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestDispatchWriteFailureKeepsTaskQueuedUntilRetry(t *testing.T) {
	config := fixtures.Config()
	store := fakes.NewDispatchFailureStore(config.OperationStore)
	config.OperationStore = store
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)

	task := fixtures.Assign(t, r, op.ID, "inspect")
	select {
	case <-store.Attempted():
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

	store.Allow()
	call := fixtures.Receive(t, calls)
	reserved, err := r.Get(t.Context(), op.ID)
	if err != nil || len(reserved.Tasks) != 1 || reserved.Tasks[0].Status != operations.TaskRunning || reserved.Tasks[0].CommandID != task.ID || reserved.Tasks[0].StartedAt.IsZero() || reserved.Tasks[0].DeadlineAt == nil {
		t.Fatalf("retry executed without a complete reservation: %+v, %v", reserved, err)
	}
	if call.Agent != reserved.Tasks[0].AgentID || task.AgentID != "" || call.Call.Work.TaskID != task.ID || len(reserved.Events) != len(events)+1 || reserved.Events[len(events)].Type != operations.TaskDispatched {
		t.Fatalf("retry changed or duplicated the Dispatch: call=%+v, operation=%+v", call, reserved)
	}
	call.Reply <- fixtures.Output("inspect")
	fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
}
