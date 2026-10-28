package runner_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestAssignmentWriteFailurePreservesRetryableState(t *testing.T) {
	config := fixtures.Config()
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	failure := errors.New("database unavailable")
	config.OperationStore.(*fakes.OperationStore).FailNextUpdate(failure)
	command := commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect"}

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
	call := fixtures.Receive(t, calls)
	call.Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if len(settled.Tasks) != 1 || settled.Tasks[0].ID != task.ID || call.Call.Work.TaskID != task.ID {
		t.Fatalf("retry lost or duplicated the assignment: %+v", settled)
	}
}

func TestAssignmentReturnsRequestedTaskFromStoreSnapshot(t *testing.T) {
	config := fixtures.Config()
	config.OperationStore = &fakes.AdditionalQueuedTaskStore{OperationStore: config.OperationStore}
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)

	task := fixtures.Assign(t, r, op.ID, "inspect")
	stored, err := r.Get(t.Context(), op.ID)
	if err != nil || len(stored.Tasks) != 2 || task.ID != stored.Tasks[0].ID || task.CommandID != stored.Tasks[0].CommandID || task.ActionName != "inspect" {
		t.Fatalf("assignment returned a different queued task: task=%+v, operation=%+v, error=%v", task, stored, err)
	}
	if call := fixtures.Receive(t, calls); call.Call.Work.TaskID != task.ID {
		t.Fatalf("dispatched a different assignment: %+v", call)
	}
}

func TestControllerSelectsReadyAgentsAndRespectsCapacity(t *testing.T) {
	config := fixtures.Config()
	unavailable := fixtures.ReadyAgent(fixtures.AgentID)
	unavailable.Actions[0].Status = actions.Unavailable
	first, second := fixtures.ReadyAgent("review-agent-00000002"), fixtures.ReadyAgent("review-agent-00000003")
	stale := fixtures.ReadyAgent("review-agent-00000005")
	stale.Stale = true
	busy := fixtures.ReadyAgent("review-agent-00000006")
	busy.Status = agents.AgentWorking
	config.AgentCatalog.(*fakes.AgentCatalog).Set(second, unavailable, stale, busy, first)
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)

	for range 3 {
		task := fixtures.Assign(t, r, op.ID, "inspect")
		if task.AgentID != "" || task.Status != operations.TaskQueued {
			t.Fatalf("assignment selected an agent before Dispatch: %+v", task)
		}
	}
	firstCall, secondCall := fixtures.Receive(t, calls), fixtures.Receive(t, calls)
	current, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}

	eligible := map[string]bool{first.ID: true, second.ID: true}
	if firstCall.Agent == secondCall.Agent || !eligible[firstCall.Agent] || !eligible[secondCall.Agent] {
		t.Fatalf("controller selected an unavailable or occupied Agent: %+v, %+v", firstCall, secondCall)
	}
	var queued operations.Task
	running := map[string]string{}
	for _, task := range current.Tasks {
		if task.Status == operations.TaskQueued {
			queued = task
		} else {
			running[task.ID] = task.AgentID
		}
	}
	if queued.ID == "" || queued.AgentID != "" || len(running) != 2 || running[firstCall.Call.Work.TaskID] != firstCall.Agent || running[secondCall.Call.Work.TaskID] != secondCall.Agent {
		t.Fatalf("dispatch did not persist actual executors or exceeded capacity: %+v", current.Tasks)
	}

	firstCall.Reply <- fixtures.Output("inspect")
	resumed := fixtures.Receive(t, calls)
	if resumed.Call.Work.TaskID != queued.ID || resumed.Agent != firstCall.Agent {
		t.Fatalf("queued task did not use the agent whose capacity became free: %+v", resumed)
	}
	secondCall.Reply <- fixtures.Output("inspect")
	resumed.Reply <- fixtures.Output("inspect")
	fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		for _, task := range op.Tasks {
			if task.Status != operations.TaskSucceeded {
				return false
			}
		}
		return true
	})
}
