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
)

func TestRulesDefaultRunsEveryProposedTaskIndependently(t *testing.T) {
	config := fixture()
	config.AgentCatalog.(*agentCatalog).set(readyAgent(agentID), readyAgent("review-agent-00000002"))
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	parent := assign(t, r, op.ID, "discover")
	proposals := []actions.NextAction{
		{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
		{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)},
	}
	receive(t, calls).reply <- output("discover", proposals...)
	first, second := receive(t, calls), receive(t, calls)
	if first.call.Work.TaskID == second.call.Work.TaskID {
		t.Fatal("repeated action names must create separate tasks")
	}
	inputs := map[string]bool{string(first.call.Input): true, string(second.call.Input): true}
	for _, proposal := range proposals {
		if !inputs[string(proposal.Input)] {
			t.Fatalf("proposal input was lost: %s; dispatches: %+v, %+v", proposal.Input, first.call, second.call)
		}
	}
	first.reply <- output("inspect")
	partial := awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		for _, task := range op.Tasks {
			if task.ID == first.call.Work.TaskID {
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
		if child.ID == first.call.Work.TaskID && (child.Continuation == nil || child.Continuation.DecisionBy != operations.DecisionModel || child.Continuation.Status != operations.ContinuationPending) {
			t.Fatalf("first child waited for its sibling without an aggregation rule: %+v", child)
		}
	}
	second.reply <- output("inspect")
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	for _, child := range settled.Tasks[1:] {
		if child.Continuation == nil || child.Continuation.DecisionBy != operations.DecisionModel || child.Continuation.Status != operations.ContinuationPending {
			t.Fatalf("expected an independent decision for each completed child: %+v", settled)
		}
	}
}

func TestRulesModelDecisionStopsProposedTasks(t *testing.T) {
	config := fixture()
	config.MissionStore.(*missionStore).mission.Rules = missions.RuleSet{
		missions.DecideNextActions: {Actions: []string{"discover"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	assign(t, r, op.ID, "discover")
	proposed := actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)}
	receive(t, calls).reply <- output("discover", proposed)
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
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
	config := fixture()
	config.AgentCatalog.(*agentCatalog).set(readyAgent(agentID), readyAgent("review-agent-00000002"))
	config.MissionStore.(*missionStore).mission.Rules = missions.RuleSet{
		missions.AggregateResults:  {Actions: []string{"discover"}},
		missions.DecideNextActions: {Actions: []string{"discover", "inspect"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	parent := assign(t, r, op.ID, "discover")
	proposals := []actions.NextAction{
		{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
		{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)},
	}
	receive(t, calls).reply <- output("discover", proposals...)
	initial := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if len(initial.Tasks) != 1 || initial.Tasks[0].Continuation.DecisionBy != operations.DecisionModel {
		t.Fatalf("aggregation bypassed initial model review: %+v", initial)
	}
	_, err := r.Resolve(t.Context(), commands.ResolveContinuationCommand{
		OperationID: op.ID, TaskID: parent.ID, Version: initial.Tasks[0].Continuation.Version, Actions: proposals,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, second := receive(t, calls), receive(t, calls)
	first.reply <- output("inspect", actions.NextAction{ActionName: "follow-up", Input: first.call.Input})
	partial := awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return len(op.Tasks) == 3 && (op.Tasks[1].Status == operations.TaskSucceeded || op.Tasks[2].Status == operations.TaskSucceeded)
	})
	if c := partial.Tasks[0].Continuation; c.DecisionBy != operations.DecisionCode || c.Status != operations.ContinuationWaiting || ruleDecisionCount(partial) != 0 {
		t.Fatalf("model-selected children escaped the aggregation group: %+v", partial)
	}
	second.reply <- output("inspect", actions.NextAction{ActionName: "follow-up", Input: second.call.Input})
	grouped := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
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
	third := receive(t, calls)
	current, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := current.Tasks[0].Continuation
	if len(current.Tasks) != 4 || c.Status != operations.ContinuationWaiting || !reflect.DeepEqual(c.CreatedTaskIDs, []string{third.call.Work.TaskID}) {
		t.Fatalf("new model decision reused an earlier aggregation group: %+v", current)
	}
	third.reply <- output("inspect", actions.NextAction{ActionName: "follow-up", Input: third.call.Input})
	final := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	if ruleDecisionCount(final) != 2 || len(final.Tasks) != 4 || !reflect.DeepEqual(final.Tasks[0].Continuation.ProposedActions, []actions.NextAction{{ActionName: "follow-up", Input: json.RawMessage(`{"page":3}`)}}) {
		t.Fatalf("later group repeated old proposals or lost its own result: %+v", final)
	}
}

func TestRulesOperationKeepsItsMissionSnapshot(t *testing.T) {
	config := fixture()
	catalog := config.MissionStore.(*missionStore)
	catalog.mission.Rules = missions.RuleSet{
		missions.AggregateResults:  {Actions: []string{"inspect"}},
		missions.DecideNextActions: {Actions: []string{"discover"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
	expected := catalog.mission.Rules.Clone()
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	catalog.mission.Rules[missions.DecideNextActions].Actions[0] = "inspect"
	catalog.mission.Rules[missions.DecideNextActions].Params.DecisionBy = "changed"
	delete(catalog.mission.Rules, missions.AggregateResults)
	op.Mission.Rules[missions.DecideNextActions].Actions[0] = "returned snapshot changed"
	op.Mission.Rules[missions.DecideNextActions].Params.DecisionBy = "changed"
	assign(t, r, op.ID, "discover")
	receive(t, calls).reply <- output("discover", actions.NextAction{ActionName: "inspect"})
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
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
	config := fixture()
	config.AgentCatalog.(*agentCatalog).set(readyAgent(agentID), readyAgent("review-agent-00000002"))
	config.MissionStore.(*missionStore).mission.Rules = missions.RuleSet{
		missions.AggregateResults: {Actions: []string{"discover"}},
	}
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	parent := assign(t, r, op.ID, "discover")

	receive(t, calls).reply <- output("discover",
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)})
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
	receive(t, calls)
	committed, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Tasks) != 2 || committed.Tasks[0].Continuation.Status != operations.ContinuationResolved {
		t.Fatalf("save retry failed: %+v", committed)
	}
}
