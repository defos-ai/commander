package runner_test

import (
	"encoding/json"
	"errors"
	"testing"

	"commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/actions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
)

func TestAutomationWaitsForAllChildrenBeforeModelReview(t *testing.T) {
	config := fixture()
	config.MissionStore.(*missionStore).mission.Automations = map[string]missions.Automation{
		"discover": {Run: []string{"inspect"}, Continue: missions.WhenAll},
	}
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	parent := assign(t, r, op.ID, "discover")

	receive(t, calls).reply <- output("discover",
		actions.NextAction{ActionName: "inspect", AgentID: agentID, Input: json.RawMessage(`{"page":1}`)},
		actions.NextAction{ActionName: "inspect", AgentID: "review-agent-00000002", Input: json.RawMessage(`{"page":2}`)})
	first, second := receive(t, calls), receive(t, calls)
	first.reply <- output("inspect")
	partial := awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return len(op.Tasks) == 3 && (op.Tasks[1].Status == operations.TaskSucceeded || op.Tasks[2].Status == operations.TaskSucceeded)
	})
	if first.agent == second.agent || partial.Tasks[0].Continuation.Status != operations.ContinuationWaiting {
		t.Fatalf("fanout failed or model saw an unfinished group: %+v", partial)
	}
	second.reply <- output("inspect")
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })

	ready := 0
	for _, event := range settled.Events {
		if event.Type == operations.ContinuationReady {
			ready++
		}
	}
	c := settled.Tasks[0].Continuation
	if ready != 1 || len(settled.Tasks) != 3 || len(c.CreatedTaskIDs) != 2 || c.DecisionBy != operations.DecisionModel || c.Status != operations.ContinuationPending {
		t.Fatalf("expected one grouped model decision: %+v", settled)
	}
	for _, child := range settled.Tasks[1:] {
		if child.ParentTaskID != parent.ID || child.Result == nil || child.Continuation.Status != operations.ContinuationResolved {
			t.Fatalf("child escaped grouped review: %+v", child)
		}
	}
}

func TestConcurrentContinuationResolutionCommitsOneBranch(t *testing.T) {
	exec, calls := controlledExecutor()
	r := newRunner(t, fixture(), exec)
	op := start(t, r)
	task := assign(t, r, op.ID, "inspect")
	receive(t, calls).reply <- output("inspect")
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	command := commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: task.ID, Version: settled.Tasks[0].Continuation.Version,
		Actions: []actions.NextAction{{ActionName: "follow-up", AgentID: agentID}}}
	gate, results := make(chan struct{}), make(chan error, 2)

	for range 2 {
		go func() { <-gate; _, err := r.Resolve(t.Context(), command); results <- err }()
	}
	close(gate)
	a, b := <-results, <-results
	_, stale := r.Resolve(t.Context(), command)

	if (a == nil) == (b == nil) || stale == nil {
		t.Fatalf("conflicting decisions: %v, %v, replay=%v", a, b, stale)
	}
	child := receive(t, calls)
	current, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Tasks) != 2 || current.Tasks[1].ID != child.call.Work.TaskID || current.Tasks[1].ParentTaskID != task.ID {
		t.Fatalf("duplicate child or missing parent link: %+v", current.Tasks)
	}
}

func TestFailedDecisionSavePreservesRetryableState(t *testing.T) {
	config := fixture()
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	task := assign(t, r, op.ID, "inspect")
	receive(t, calls).reply <- output("inspect")
	before := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	store := config.OperationStore.(*operationStore)
	failure := errors.New("database unavailable")
	store.failNextUpdate(failure)
	command := commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: task.ID, Version: before.Tasks[0].Continuation.Version,
		Actions: []actions.NextAction{{ActionName: "follow-up", AgentID: agentID}}}

	_, err := r.Resolve(t.Context(), command)
	after, readErr := r.Get(t.Context(), op.ID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !errors.Is(err, failure) || !after.UpdatedAt.Equal(before.UpdatedAt) || len(after.Tasks) != 1 || after.Tasks[0].Continuation.Status != operations.ContinuationPending {
		t.Fatalf("failed save changed durable state: %+v, error=%v", after, err)
	}
	select {
	case call := <-calls:
		t.Fatalf("uncommitted command dispatched: %+v", call)
	default:
	}
	_, err = r.Resolve(t.Context(), command)

	if err != nil {
		t.Fatal(err)
	}
	receive(t, calls)
	committed, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Tasks) != 2 || committed.Tasks[0].Continuation.Status != operations.ContinuationResolved {
		t.Fatalf("save retry failed: %+v", committed)
	}
}
