package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestSelectedAffinitySelectsAnAgentSupportingTheWholeGroup(t *testing.T) {
	config := fixture()
	selected := missions.RuleSet{missions.SameAgent: {ForActions: []string{"inspect", "discover"}}}
	config.RuleSelector = ruleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
		return selected, nil
	})
	partial, complete := readyAgent(agentID), readyAgent("review-agent-00000002")
	partial.Actions = []actions.Action{
		{Name: "inspect", Status: actions.Ready},
		{Name: "follow-up", Status: actions.Ready},
	}
	config.AgentCatalog.(*agentCatalog).set(partial, complete)
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	if !reflect.DeepEqual(op.Mission.Rules, selected) || len(config.MissionStore.(*missionStore).mission.Rules) != 0 {
		t.Fatal("selected affinity must belong to the saved operation without changing the mission catalog")
	}

	first := assign(t, r, op.ID, "inspect")
	firstCall := receive(t, calls)
	if firstCall.agent != complete.ID || firstCall.call.Work.TaskID != first.ID {
		t.Fatalf("affinity bound to an agent missing another grouped action: %+v", firstCall)
	}

	unrelated := assign(t, r, op.ID, "follow-up")
	unrelatedCall := receive(t, calls)
	if unrelatedCall.agent != partial.ID || unrelatedCall.call.Work.TaskID != unrelated.ID {
		t.Fatalf("affinity restricted an unrelated action: %+v", unrelatedCall)
	}
	unrelatedCall.reply <- output("follow-up")
	firstCall.reply <- output("inspect", actions.NextAction{ActionName: "discover", Input: json.RawMessage(`{}`)})
	next := receive(t, calls)
	if next.agent != complete.ID || next.call.ActionName != "discover" || next.call.Work.OperationID != op.ID {
		t.Fatalf("automatic follow-up lost affinity: %+v", next)
	}
	next.reply <- output("discover")
	awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return len(op.Tasks) == 3 && op.Tasks[0].Status == operations.TaskSucceeded &&
			op.Tasks[1].Status == operations.TaskSucceeded && op.Tasks[2].Status == operations.TaskSucceeded
	})
}

func TestAffinityQueuesBusySiblingsWithoutPinningOtherOperations(t *testing.T) {
	config := affinityRunnerConfig()
	config.Capacity = 2
	first, second := readyAgent(agentID), readyAgent("review-agent-00000002")
	config.AgentCatalog.(*agentCatalog).set(first, second)
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	assign(t, r, op.ID, "follow-up")
	receive(t, calls).reply <- output("follow-up",
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{}`)},
		actions.NextAction{ActionName: "discover", Input: json.RawMessage(`{}`)},
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{}`)})

	grouped, concurrent := receive(t, calls), receive(t, calls)
	if grouped.agent != concurrent.agent || grouped.call.ActionName == concurrent.call.ActionName ||
		grouped.call.Work.OperationID != op.ID || concurrent.call.Work.OperationID != op.ID {
		t.Fatalf("concurrent grouped actions did not share an owner: %+v, %+v", grouped, concurrent)
	}
	// One result creates all children. Affinity must apply immediately without
	// serializing the first two or sending the third to the spare agent.
	stored := assertAffinityTaskQueued(t, r, op.ID, 3, calls)
	for _, call := range []invocation{grouped, concurrent} {
		if call.call.Work.TaskID != stored.Tasks[1].ID && call.call.Work.TaskID != stored.Tasks[2].ID {
			t.Fatalf("concurrent dispatch changed its task: %+v", call)
		}
	}

	other := start(t, r)
	otherTask := assign(t, r, other.ID, "inspect")
	otherCall := receive(t, calls)
	if otherCall.agent == grouped.agent || otherCall.call.Work.TaskID != otherTask.ID {
		t.Fatalf("independent operation could not use the spare agent: %+v", otherCall)
	}
	otherCall.reply <- output("inspect")
	awaitOperation(t, r, other.ID, func(op operations.Operation) bool {
		return op.Tasks[0].Status == operations.TaskSucceeded
	})
	assertAffinityTaskQueued(t, r, op.ID, 3, calls)

	grouped.reply <- output(grouped.call.ActionName)
	sibling := receive(t, calls)
	if sibling.agent != grouped.agent || sibling.call.Work.TaskID != stored.Tasks[3].ID {
		t.Fatalf("queued sibling moved away from its owner: %+v", sibling)
	}
	sibling.reply <- output("inspect")
	concurrent.reply <- output(concurrent.call.ActionName)
	awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return op.Tasks[1].Status == operations.TaskSucceeded && op.Tasks[2].Status == operations.TaskSucceeded &&
			op.Tasks[3].Status == operations.TaskSucceeded
	})
}

func TestAffinityRecoveryWaitsForTheOriginalAgent(t *testing.T) {
	config := fixture()
	var selections atomic.Int32
	selected := missions.RuleSet{missions.SameAgent: {ForActions: []string{"inspect", "discover"}}}
	config.RuleSelector = ruleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
		selections.Add(1)
		return selected, nil
	})
	owner, spare := readyAgent(agentID), readyAgent("review-agent-00000002")
	catalog := config.AgentCatalog.(*agentCatalog)
	catalog.set(owner, spare)
	exec, calls := controlledExecutor()
	first := newRunner(t, config, exec)
	op := start(t, first)
	originalTask := assign(t, first, op.ID, "inspect")
	original := receive(t, calls)
	if original.agent != owner.ID {
		t.Fatalf("unexpected initial agent: %+v", original)
	}
	queued := assign(t, first, op.ID, "discover")
	assertAffinityTaskQueued(t, first, op.ID, 1, calls)
	closeRunner(t, first)
	config.RuleSelector = ruleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
		selections.Add(1)
		return nil, errors.New("must not reselect affinity for a saved operation")
	})

	owner.Stale = true
	catalog.set(owner, spare)
	observed := make(chan invocation, 1)
	exec.recover = func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		select {
		case observed <- invocation{agent: agent, call: call}:
			return output(call.ActionName), nil
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
	}
	restarted := newRunner(t, config, exec)
	recovered := receive(t, observed)
	if recovered.agent != original.agent || recovered.call.Work != original.call.Work {
		t.Fatalf("recovery changed the saved dispatch: %+v", recovered)
	}
	offline := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool {
		return op.Tasks[0].Status == operations.TaskSucceeded && op.Preflight != nil && !op.Preflight.Ready
	})
	assertAffinityTaskQueued(t, restarted, op.ID, 1, calls)

	// A live owner with an unavailable action still cannot delegate its files to
	// the ready spare. Wait for the saved preflight change to observe rediscovery.
	owner.Stale = false
	owner.Actions[1].Status = actions.Unavailable
	owner.Actions[1].MissingRequirements = []actions.Requirement{{Kind: actions.ExecutableRequirement, Name: "git"}}
	catalog.set(owner, spare)
	awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool {
		return op.Preflight.Version > offline.Preflight.Version && !op.Preflight.Ready
	})
	assertAffinityTaskQueued(t, restarted, op.ID, 1, calls)

	owner.Actions[1].Status = actions.Ready
	owner.Actions[1].MissingRequirements = nil
	catalog.set(owner, spare)
	resumed := receive(t, calls)
	if resumed.agent != owner.ID || resumed.call.Work.TaskID != queued.ID {
		t.Fatalf("returning owner did not resume its queued task: %+v", resumed)
	}
	resumed.reply <- output("discover")
	settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool {
		return op.Tasks[1].Status == operations.TaskSucceeded
	})
	dispatches := 0
	for _, event := range settled.Events {
		if event.Type == operations.TaskDispatched && event.TaskID == originalTask.ID {
			dispatches++
		}
	}
	if dispatches != 1 || len(settled.Tasks) != 2 || settled.Tasks[0].ID != originalTask.ID {
		t.Fatalf("recovery replayed the original task: %+v", settled)
	}
	if selections.Load() != 1 || !reflect.DeepEqual(settled.Mission.Rules, selected) ||
		len(config.MissionStore.(*missionStore).mission.Rules) != 0 {
		t.Fatal("recovery must retain selected affinity without reselecting or changing the mission catalog")
	}
	select {
	case call := <-calls:
		t.Fatalf("recovery created an extra execution: %+v", call)
	case call := <-observed:
		t.Fatalf("recovery observed the same execution twice: %+v", call)
	default:
	}
}

func affinityRunnerConfig() runner.Config {
	config := fixture()
	config.MissionStore.(*missionStore).mission.Rules = missions.RuleSet{
		missions.SameAgent: {ForActions: []string{"inspect", "discover"}},
	}
	return config
}

func assertAffinityTaskQueued(t *testing.T, r *runner.Runner, operationID string, index int, calls <-chan invocation) operations.Operation {
	t.Helper()
	// Events uses the runner owner loop, so this observes a complete scheduler pass.
	if _, err := r.Events(t.Context(), operationID, 0, false); err != nil {
		t.Fatal(err)
	}
	op, err := r.Get(t.Context(), operationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(op.Tasks) <= index || op.Tasks[index].Status != operations.TaskQueued || op.Tasks[index].AgentID != "" {
		t.Fatalf("affinity wait did not retain an unassigned queued task: %+v", op)
	}
	select {
	case call := <-calls:
		t.Fatalf("waiting task dispatched to another agent: %+v", call)
	default:
	}
	return op
}
