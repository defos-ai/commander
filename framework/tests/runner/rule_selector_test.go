package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type ruleSelectorFunc func(context.Context, runner.RuleSelection) (missions.RuleSet, error)

func (f ruleSelectorFunc) Select(ctx context.Context, selection runner.RuleSelection) (missions.RuleSet, error) {
	return f(ctx, selection)
}

func TestRuleSelectorSavesIndependentRulesAndInputs(t *testing.T) {
	config := fixture()
	catalog := config.MissionStore.(*missionStore)
	catalog.mission.Rules = modelRules("inspect")
	definition := catalog.mission.Clone()
	selected := modelRules("discover")
	wantRules := selected.Clone()
	input := json.RawMessage(`{"page":1}`)
	var selections atomic.Int32
	config.RuleSelector = ruleSelectorFunc(func(_ context.Context, selection runner.RuleSelection) (missions.RuleSet, error) {
		selections.Add(1)
		if !reflect.DeepEqual(selection.Mission, definition) || string(selection.Input) != string(input) {
			t.Errorf("selector received the wrong context: %+v", selection)
		}
		selection.Input[0] = '['
		selection.Mission.Objective = "modified"
		selection.Mission.InputSchema[0] = '['
		selection.Mission.Rules[missions.DecideNextActions].ForActions[0] = "modified"
		selection.Mission.Rules[missions.DecideNextActions].Params.DecisionBy = "modified"
		return selected, nil
	})
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op, err := r.Start(t.Context(), commands.StartOperationCommand{MissionName: "review", Input: input})
	if err != nil {
		t.Fatal(err)
	}
	if string(input) != `{"page":1}` || !reflect.DeepEqual(catalog.mission, definition) {
		t.Fatal("selector modified caller input or the mission catalog")
	}
	selected[missions.DecideNextActions].ForActions[0] = "modified"
	selected[missions.DecideNextActions].Params.DecisionBy = "modified"
	delete(selected, missions.DecideNextActions)
	op.Mission.Rules[missions.DecideNextActions].ForActions[0] = "returned snapshot modified"
	input[0] = '['
	assign(t, r, op.ID, "discover")
	receive(t, calls).reply <- output("discover", actions.NextAction{ActionName: "inspect"})
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	definition.Rules = wantRules
	if !reflect.DeepEqual(settled.Mission, definition) || string(settled.Input) != `{"page":1}` {
		t.Fatalf("selected rules or operation inputs were not isolated: %+v", settled)
	}
	if len(settled.Tasks) != 1 || settled.Tasks[0].Continuation.DecisionBy != operations.DecisionModel || selections.Load() != 1 {
		t.Fatalf("selected model rule was not applied once: operation=%+v, selections=%d", settled, selections.Load())
	}
}

func TestRuleSelectorCanClearMissionRules(t *testing.T) {
	config := fixture()
	config.MissionStore.(*missionStore).mission.Rules = modelRules("discover")
	config.RuleSelector = ruleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
		return nil, nil
	})
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	parent := assign(t, r, op.ID, "discover")
	receive(t, calls).reply <- output("discover", actions.NextAction{ActionName: "inspect"})
	child := receive(t, calls)
	child.reply <- output("inspect")
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return len(op.Tasks) == 2 && op.Tasks[1].Status == operations.TaskSucceeded
	})
	if settled.Mission.Rules != nil || settled.Tasks[1].ParentTaskID != parent.ID || settled.Tasks[0].Continuation.DecisionBy != operations.DecisionCode {
		t.Fatalf("nil selected rules did not restore automatic proposals: %+v", settled)
	}
}

func TestRuleSelectionFailsBeforeSavingAnOperation(t *testing.T) {
	selectionFailure := errors.New("rule selection unavailable")
	for _, test := range []struct {
		name       string
		mission    func(*missions.Mission)
		rules      missions.RuleSet
		err        error
		selections int32
	}{
		{name: "invalid mission", mission: func(m *missions.Mission) { m.Objective = "" }},
		{name: "invalid input", mission: func(m *missions.Mission) {
			m.InputSchema = json.RawMessage(`{"type":"object","required":["target"]}`)
		}},
		{name: "selector error", err: selectionFailure, selections: 1},
		{name: "unknown rule", rules: missions.RuleSet{"unknown": {ForActions: []string{"discover"}}}, selections: 1},
		{name: "invalid rule parameters", rules: missions.RuleSet{missions.DecideNextActions: {ForActions: []string{"discover"}}}, selections: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := fixture()
			if test.mission != nil {
				test.mission(&config.MissionStore.(*missionStore).mission)
			}
			var selections atomic.Int32
			config.RuleSelector = ruleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
				selections.Add(1)
				return test.rules, test.err
			})
			r := newRunner(t, config, executor{})
			_, err := r.Start(t.Context(), commands.StartOperationCommand{MissionName: "review", Input: json.RawMessage(`{}`)})
			if err == nil || (test.err != nil && !errors.Is(err, test.err)) || selections.Load() != test.selections {
				t.Fatalf("invalid start: error=%v, selections=%d; want selections=%d", err, selections.Load(), test.selections)
			}
			stored, err := r.List(t.Context())
			if err != nil || len(stored) != 0 {
				t.Fatalf("failed selection saved an operation: %+v, %v", stored, err)
			}
		})
	}
}

func TestSlowRuleSelectionAllowsSettlementAndConcurrentStarts(t *testing.T) {
	config := fixture()
	entered := make(chan struct{}, 1)
	config.RuleSelector = ruleSelectorFunc(func(ctx context.Context, selection runner.RuleSelection) (missions.RuleSet, error) {
		if string(selection.Input) == `{"wait":true}` {
			entered <- struct{}{}
			<-ctx.Done()
			return nil, nil
		}
		return selection.Mission.Rules, nil
	})
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	assign(t, r, op.ID, "inspect")
	running := receive(t, calls)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, err := r.Start(ctx, commands.StartOperationCommand{MissionName: "review", Input: json.RawMessage(`{"wait":true}`)})
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("cancelled rule selection did not return")
		}
	})
	waitSelectionAttempt(t, entered)
	running.reply <- output("inspect")
	awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	concurrent := start(t, r)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled selection returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled selection did not return")
	}
	stored, err := r.List(t.Context())
	if err != nil || len(stored) != 2 || concurrent.ID == op.ID {
		t.Fatalf("slow selection blocked or persisted cancelled work: %+v, %v", stored, err)
	}
}

func TestClosingRunnerCancelsRuleSelection(t *testing.T) {
	config := fixture()
	entered := make(chan struct{}, 1)
	config.RuleSelector = ruleSelectorFunc(func(ctx context.Context, _ runner.RuleSelection) (missions.RuleSet, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return modelRules("inspect"), nil
	})
	r := newRunner(t, config, executor{})
	done := make(chan error, 1)
	go func() {
		_, err := r.Start(t.Context(), commands.StartOperationCommand{MissionName: "review", Input: json.RawMessage(`{}`)})
		done <- err
	}()
	waitSelectionAttempt(t, entered)
	closeRunner(t, r)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) && !errors.Is(err, runner.ErrClosed) {
			t.Fatalf("shutdown allowed selection to complete: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel rule selection")
	}
	stored, err := r.List(t.Context())
	if err != nil || len(stored) != 0 {
		t.Fatalf("shutdown saved an operation after cancelling selection: %+v, %v", stored, err)
	}
}

func TestRecoveryKeepsSelectedRulesWithoutReselection(t *testing.T) {
	config := fixture()
	var selections atomic.Int32
	config.RuleSelector = ruleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
		selections.Add(1)
		return modelRules("inspect"), nil
	})
	exec, calls := controlledExecutor()
	first := newRunner(t, config, exec)
	op := start(t, first)
	task := assign(t, first, op.ID, "inspect")
	receive(t, calls)
	closeRunner(t, first)
	config.RuleSelector = ruleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
		selections.Add(1)
		return nil, errors.New("must not select rules for saved operations")
	})
	exec.recover = func(_ context.Context, _ string, call actions.ActionCall) (actions.ActionOutput, error) {
		return output(call.ActionName, actions.NextAction{ActionName: "follow-up"}), nil
	}
	restarted := newRunner(t, config, exec)
	settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	c := settled.Tasks[0].Continuation
	if len(settled.Tasks) != 1 || c.DecisionBy != operations.DecisionModel || !reflect.DeepEqual(settled.Mission.Rules, modelRules("inspect")) {
		t.Fatalf("recovery discarded the selected rules: %+v", settled)
	}
	_, err := restarted.Resolve(t.Context(), commands.ResolveContinuationCommand{
		OperationID: op.ID, TaskID: task.ID, Version: c.Version,
		Actions: []actions.NextAction{{ActionName: "follow-up"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	receive(t, calls).reply <- output("follow-up")
	awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool {
		return len(op.Tasks) == 2 && op.Tasks[1].Status == operations.TaskSucceeded
	})
	if selections.Load() != 1 {
		t.Fatalf("recovery or continuation execution selected rules again: %d selections", selections.Load())
	}
}

func modelRules(action string) missions.RuleSet {
	return missions.RuleSet{
		missions.DecideNextActions: {ForActions: []string{action}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
}
