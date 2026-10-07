package runner_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestAssignmentWriteFailurePreservesRetryableState(t *testing.T) {
	config := fixture()
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	failure := errors.New("database unavailable")
	config.OperationStore.(*operationStore).failNextUpdate(failure)
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
	later := operations.NewTask(operationID, "", "", actions.NextAction{ActionName: "follow-up"})
	return s.OperationStore.QueueTask(ctx, operationID, later)
}

func TestControllerSelectsReadyAgentsAndRespectsCapacity(t *testing.T) {
	config := fixture()
	unavailable := readyAgent(agentID)
	unavailable.Actions[0].Status = actions.Unavailable
	first, second := readyAgent("review-agent-00000002"), readyAgent("review-agent-00000003")
	denied := readyAgent("review-agent-00000004")
	denied.Actions[0].RequiredPermissions = []string{"network-access"}
	stale := readyAgent("review-agent-00000005")
	stale.Stale = true
	busy := readyAgent("review-agent-00000006")
	busy.Status = agents.AgentWorking
	config.AgentCatalog.(*agentCatalog).set(denied, second, unavailable, stale, busy, first)
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)

	for range 3 {
		task := assign(t, r, op.ID, "inspect")
		if task.AgentID != "" || task.Status != operations.TaskQueued {
			t.Fatalf("assignment selected an agent before dispatch: %+v", task)
		}
	}
	firstCall, secondCall := receive(t, calls), receive(t, calls)
	current, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}

	eligible := map[string]bool{first.ID: true, second.ID: true}
	if firstCall.agent == secondCall.agent || !eligible[firstCall.agent] || !eligible[secondCall.agent] {
		t.Fatalf("controller selected an unavailable or occupied agent: %+v, %+v", firstCall, secondCall)
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
	if queued.ID == "" || queued.AgentID != "" || len(running) != 2 || running[firstCall.call.Work.TaskID] != firstCall.agent || running[secondCall.call.Work.TaskID] != secondCall.agent {
		t.Fatalf("dispatch did not persist actual executors or exceeded capacity: %+v", current.Tasks)
	}

	firstCall.reply <- output("inspect")
	resumed := receive(t, calls)
	if resumed.call.Work.TaskID != queued.ID || resumed.agent != firstCall.agent {
		t.Fatalf("queued task did not use the agent whose capacity became free: %+v", resumed)
	}
	secondCall.reply <- output("inspect")
	resumed.reply <- output("inspect")
	awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		for _, task := range op.Tasks {
			if task.Status != operations.TaskSucceeded {
				return false
			}
		}
		return true
	})
}
