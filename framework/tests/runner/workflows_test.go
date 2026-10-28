package runner_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestRulesDefaultRunsEveryProposedTaskIndependently(t *testing.T) {
	config := fixtures.Config()
	config.AgentCatalog.(*fakes.AgentCatalog).Set(fixtures.ReadyAgent(fixtures.AgentID), fixtures.ReadyAgent("review-agent-00000002"))
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	parent := fixtures.Assign(t, r, op.ID, "discover")
	proposals := []actions.NextAction{
		{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
		{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)},
	}
	fixtures.Receive(t, calls).Reply <- fixtures.Output("discover", proposals...)
	first, second := fixtures.Receive(t, calls), fixtures.Receive(t, calls)
	if first.Call.Work.TaskID == second.Call.Work.TaskID {
		t.Fatal("repeated action names must create separate tasks")
	}
	inputs := map[string]bool{string(first.Call.Input): true, string(second.Call.Input): true}
	for _, proposal := range proposals {
		if !inputs[string(proposal.Input)] {
			t.Fatalf("proposal input was lost: %s; dispatches: %+v, %+v", proposal.Input, first.Call, second.Call)
		}
	}
	first.Reply <- fixtures.Output("inspect")
	partial := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		for _, task := range op.Tasks {
			if task.ID == first.Call.Work.TaskID {
				return task.Status == operations.TaskSucceeded
			}
		}
		return false
	})
	if len(partial.Tasks) != 3 || partial.Tasks[0].Continuation.DecisionBy != operations.DecisionCode || partial.Tasks[0].Continuation.Status != operations.ContinuationResolved {
		t.Fatalf("default rule did not schedule proposals automatically: %+v", partial)
	}
	for _, child := range partial.Tasks[1:] {
		if child.ParentTaskID != parent.ID || child.ActionName != "inspect" {
			t.Fatalf("generated task lost its action or parent: %+v", child)
		}
		if child.ID == first.Call.Work.TaskID && (child.Continuation == nil || child.Continuation.DecisionBy != operations.DecisionModel || child.Continuation.Status != operations.ContinuationPending) {
			t.Fatalf("first child waited for its sibling without an aggregation rule: %+v", child)
		}
	}
	second.Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	for _, child := range settled.Tasks[1:] {
		if child.Continuation == nil || child.Continuation.DecisionBy != operations.DecisionModel || child.Continuation.Status != operations.ContinuationPending {
			t.Fatalf("expected an independent decision for each completed child: %+v", settled)
		}
	}
}

func TestRulesModelDecisionStopsProposedTasks(t *testing.T) {
	config := fixtures.Config()
	config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
		m.Rules = missions.RuleSet{
			missions.DecideNextActions: {ForActions: []string{"discover"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		}
	})
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	fixtures.Assign(t, r, op.ID, "discover")
	proposed := actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)}
	fixtures.Receive(t, calls).Reply <- fixtures.Output("discover", proposed)
	settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	c := settled.Tasks[0].Continuation
	if len(settled.Tasks) != 1 || c.DecisionBy != operations.DecisionModel || c.Status != operations.ContinuationPending || !reflect.DeepEqual(c.ProposedActions, []actions.NextAction{proposed}) {
		t.Fatalf("model rule did not retain proposals for review: %+v", settled)
	}
	select {
	case call := <-calls:
		t.Fatalf("proposal ran before a model decision: %+v", call)
	default:
	}
}

func TestRulesModelDecisionsCollectOnlyTheirOwnChildren(t *testing.T) {
	config := fixtures.Config()
	config.AgentCatalog.(*fakes.AgentCatalog).Set(fixtures.ReadyAgent(fixtures.AgentID), fixtures.ReadyAgent("review-agent-00000002"))
	config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
		m.Rules = missions.RuleSet{
			missions.AggregateResults:  {ForActions: []string{"discover"}},
			missions.DecideNextActions: {ForActions: []string{"discover", "inspect"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		}
	})
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	parent := fixtures.Assign(t, r, op.ID, "discover")
	proposals := []actions.NextAction{
		{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
		{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)},
	}
	fixtures.Receive(t, calls).Reply <- fixtures.Output("discover", proposals...)
	initial := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if len(initial.Tasks) != 1 || initial.Tasks[0].Continuation.DecisionBy != operations.DecisionModel {
		t.Fatalf("aggregation bypassed initial model review: %+v", initial)
	}
	_, err := r.Resolve(t.Context(), commands.ResolveContinuationCommand{
		OperationID: op.ID, TaskID: parent.ID, Version: initial.Tasks[0].Continuation.Version, Actions: proposals,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, second := fixtures.Receive(t, calls), fixtures.Receive(t, calls)
	first.Reply <- fixtures.Output("inspect", actions.NextAction{ActionName: "follow-up", Input: first.Call.Input})
	partial := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return len(op.Tasks) == 3 && (op.Tasks[1].Status == operations.TaskSucceeded || op.Tasks[2].Status == operations.TaskSucceeded)
	})
	if c := partial.Tasks[0].Continuation; c.DecisionBy != operations.DecisionCode || c.Status != operations.ContinuationWaiting || ruleDecisionCount(partial) != 0 {
		t.Fatalf("model-selected children escaped the aggregation group: %+v", partial)
	}
	second.Reply <- fixtures.Output("inspect", actions.NextAction{ActionName: "follow-up", Input: second.Call.Input})
	grouped := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	decision := grouped.Tasks[0].Continuation
	if len(grouped.Tasks) != 3 || ruleDecisionCount(grouped) != 1 || decision.DecisionBy != operations.DecisionModel || decision.Status != operations.ContinuationPending || len(decision.ProposedActions) != 2 {
		t.Fatalf("expected one model decision with both child proposals: %+v", grouped)
	}
	_, err = r.Resolve(t.Context(), commands.ResolveContinuationCommand{
		OperationID: op.ID, TaskID: parent.ID, Version: decision.Version,
		Actions: []actions.NextAction{{ActionName: "inspect", Input: json.RawMessage(`{"page":3}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	third := fixtures.Receive(t, calls)
	current, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := current.Tasks[0].Continuation
	if len(current.Tasks) != 4 || c.Status != operations.ContinuationWaiting || !reflect.DeepEqual(c.CreatedTaskIDs, []string{third.Call.Work.TaskID}) {
		t.Fatalf("new model decision reused an earlier aggregation group: %+v", current)
	}
	third.Reply <- fixtures.Output("inspect", actions.NextAction{ActionName: "follow-up", Input: third.Call.Input})
	final := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	if ruleDecisionCount(final) != 2 || len(final.Tasks) != 4 || !reflect.DeepEqual(final.Tasks[0].Continuation.ProposedActions, []actions.NextAction{{ActionName: "follow-up", Input: json.RawMessage(`{"page":3}`)}}) {
		t.Fatalf("later group repeated old proposals or lost its own result: %+v", final)
	}
}

func TestRulesOperationKeepsItsMissionSnapshot(t *testing.T) {
	config := fixtures.Config()
	catalog := config.MissionStore.(*fakes.MissionStore)
	catalog.Change(func(m *missions.Mission) {
		m.Rules = missions.RuleSet{
			missions.AggregateResults:  {ForActions: []string{"inspect"}},
			missions.DecideNextActions: {ForActions: []string{"discover"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		}
	})
	expected := catalog.Snapshot().Rules.Clone()
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	catalog.Change(func(m *missions.Mission) { m.Rules[missions.DecideNextActions].ForActions[0] = "inspect" })
	catalog.Change(func(m *missions.Mission) { m.Rules[missions.DecideNextActions].Params.DecisionBy = "changed" })
	catalog.Change(func(m *missions.Mission) { delete(m.Rules, missions.AggregateResults) })
	op.Mission.Rules[missions.DecideNextActions].ForActions[0] = "returned snapshot changed"
	op.Mission.Rules[missions.DecideNextActions].Params.DecisionBy = "changed"
	fixtures.Assign(t, r, op.ID, "discover")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("discover", actions.NextAction{ActionName: "inspect"})
	settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if !reflect.DeepEqual(settled.Mission.Rules, expected) || len(settled.Tasks) != 1 || settled.Tasks[0].Continuation.DecisionBy != operations.DecisionModel {
		t.Fatalf("catalog or returned snapshot edits changed the running operation's rules: %+v", settled)
	}
}

func ruleDecisionCount(op operations.Operation) int {
	count := 0
	for _, event := range op.Events {
		if event.Type == operations.ContinuationReady {
			count++
		}
	}
	return count
}

func TestRulesWaitForAllChildrenBeforeModelReview(t *testing.T) {
	config := fixtures.Config()
	config.AgentCatalog.(*fakes.AgentCatalog).Set(fixtures.ReadyAgent(fixtures.AgentID), fixtures.ReadyAgent("review-agent-00000002"))
	config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
		m.Rules = missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{"discover"}},
		}
	})
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	parent := fixtures.Assign(t, r, op.ID, "discover")

	fixtures.Receive(t, calls).Reply <- fixtures.Output("discover",
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)})
	first, second := fixtures.Receive(t, calls), fixtures.Receive(t, calls)
	first.Reply <- fixtures.Output("inspect")
	partial := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return len(op.Tasks) == 3 && (op.Tasks[1].Status == operations.TaskSucceeded || op.Tasks[2].Status == operations.TaskSucceeded)
	})
	if first.Agent == second.Agent || partial.Tasks[0].Continuation.Status != operations.ContinuationWaiting {
		t.Fatalf("fanout failed or model saw an unfinished group: %+v", partial)
	}
	second.Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })

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
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, fixtures.Config(), exec)
	op := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, op.ID, "inspect")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	command := commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: task.ID, Version: settled.Tasks[0].Continuation.Version,
		Actions: []actions.NextAction{{ActionName: "follow-up"}}}
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
	child := fixtures.Receive(t, calls)
	current, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Tasks) != 2 || current.Tasks[1].ID != child.Call.Work.TaskID || current.Tasks[1].ParentTaskID != task.ID {
		t.Fatalf("duplicate child or missing parent link: %+v", current.Tasks)
	}
}

func TestFailedDecisionSavePreservesRetryableState(t *testing.T) {
	config := fixtures.Config()
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, op.ID, "inspect")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("inspect")
	before := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	store := config.OperationStore.(*fakes.OperationStore)
	failure := errors.New("database unavailable")
	store.FailNextUpdate(failure)
	command := commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: task.ID, Version: before.Tasks[0].Continuation.Version,
		Actions: []actions.NextAction{{ActionName: "follow-up"}}}

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
	fixtures.Receive(t, calls)
	committed, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Tasks) != 2 || committed.Tasks[0].Continuation.Status != operations.ContinuationResolved {
		t.Fatalf("save retry failed: %+v", committed)
	}
}
