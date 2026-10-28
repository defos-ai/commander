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
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestSelectedAffinitySelectsAnAgentSupportingTheWholeGroup(t *testing.T) {
	config := fixtures.Config()
	selected := missions.RuleSet{missions.SameAgent: {ForActions: []string{"inspect", "discover"}}}
	config.RuleSelector = fakes.RuleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
		return selected, nil
	})
	partial, complete := fixtures.ReadyAgent(fixtures.AgentID), fixtures.ReadyAgent("review-agent-00000002")
	partial.Actions = []actions.Action{
		{Name: "inspect", Status: actions.Ready},
		{Name: "follow-up", Status: actions.Ready},
	}
	config.AgentCatalog.(*fakes.AgentCatalog).Set(partial, complete)
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	if !reflect.DeepEqual(op.Mission.Rules, selected) || len(config.MissionStore.(*fakes.MissionStore).Snapshot().Rules) != 0 {
		t.Fatal("selected affinity must belong to the saved operation without changing the mission catalog")
	}

	first := fixtures.Assign(t, r, op.ID, "inspect")
	firstCall := fixtures.Receive(t, calls)
	if firstCall.Agent != complete.ID || firstCall.Call.Work.TaskID != first.ID {
		t.Fatalf("affinity bound to an agent missing another grouped action: %+v", firstCall)
	}

	unrelated := fixtures.Assign(t, r, op.ID, "follow-up")
	unrelatedCall := fixtures.Receive(t, calls)
	if unrelatedCall.Agent != partial.ID || unrelatedCall.Call.Work.TaskID != unrelated.ID {
		t.Fatalf("affinity restricted an unrelated action: %+v", unrelatedCall)
	}
	unrelatedCall.Reply <- fixtures.Output("follow-up")
	firstCall.Reply <- fixtures.Output("inspect", actions.NextAction{ActionName: "discover", Input: json.RawMessage(`{}`)})
	next := fixtures.Receive(t, calls)
	if next.Agent != complete.ID || next.Call.ActionName != "discover" || next.Call.Work.OperationID != op.ID {
		t.Fatalf("automatic follow-up lost affinity: %+v", next)
	}
	next.Reply <- fixtures.Output("discover")
	fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return len(op.Tasks) == 3 && op.Tasks[0].Status == operations.TaskSucceeded &&
			op.Tasks[1].Status == operations.TaskSucceeded && op.Tasks[2].Status == operations.TaskSucceeded
	})
}

func TestAffinityQueuesBusySiblingsWithoutPinningOtherOperations(t *testing.T) {
	config := fixtures.AffinityConfig()
	config.Capacity = 2
	first, second := fixtures.ReadyAgent(fixtures.AgentID), fixtures.ReadyAgent("review-agent-00000002")
	config.AgentCatalog.(*fakes.AgentCatalog).Set(first, second)
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	fixtures.Assign(t, r, op.ID, "follow-up")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("follow-up",
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{}`)},
		actions.NextAction{ActionName: "discover", Input: json.RawMessage(`{}`)},
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{}`)})

	grouped, concurrent := fixtures.Receive(t, calls), fixtures.Receive(t, calls)
	if grouped.Agent != concurrent.Agent || grouped.Call.ActionName == concurrent.Call.ActionName ||
		grouped.Call.Work.OperationID != op.ID || concurrent.Call.Work.OperationID != op.ID {
		t.Fatalf("concurrent grouped actions did not share an owner: %+v, %+v", grouped, concurrent)
	}
	// One result creates all children. Affinity must apply immediately without
	// serializing the first two or sending the third to the spare agent.
	stored := assertAffinityTaskQueued(t, r, op.ID, 3, calls)
	for _, call := range []fakes.Invocation{grouped, concurrent} {
		if call.Call.Work.TaskID != stored.Tasks[1].ID && call.Call.Work.TaskID != stored.Tasks[2].ID {
			t.Fatalf("concurrent dispatch changed its task: %+v", call)
		}
	}

	other := fixtures.Start(t, r)
	otherTask := fixtures.Assign(t, r, other.ID, "inspect")
	otherCall := fixtures.Receive(t, calls)
	if otherCall.Agent == grouped.Agent || otherCall.Call.Work.TaskID != otherTask.ID {
		t.Fatalf("independent operation could not use the spare Agent: %+v", otherCall)
	}
	otherCall.Reply <- fixtures.Output("inspect")
	fixtures.AwaitOperation(t, r, other.ID, func(op operations.Operation) bool {
		return op.Tasks[0].Status == operations.TaskSucceeded
	})
	assertAffinityTaskQueued(t, r, op.ID, 3, calls)

	grouped.Reply <- fixtures.Output(grouped.Call.ActionName)
	sibling := fixtures.Receive(t, calls)
	if sibling.Agent != grouped.Agent || sibling.Call.Work.TaskID != stored.Tasks[3].ID {
		t.Fatalf("queued sibling moved away from its owner: %+v", sibling)
	}
	sibling.Reply <- fixtures.Output("inspect")
	concurrent.Reply <- fixtures.Output(concurrent.Call.ActionName)
	fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return op.Tasks[1].Status == operations.TaskSucceeded && op.Tasks[2].Status == operations.TaskSucceeded &&
			op.Tasks[3].Status == operations.TaskSucceeded
	})
}

func TestAffinityRecoveryWaitsForTheOriginalAgent(t *testing.T) {
	config := fixtures.Config()
	var selections atomic.Int32
	selected := missions.RuleSet{missions.SameAgent: {ForActions: []string{"inspect", "discover"}}}
	config.RuleSelector = fakes.RuleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
		selections.Add(1)
		return selected, nil
	})
	owner, spare := fixtures.ReadyAgent(fixtures.AgentID), fixtures.ReadyAgent("review-agent-00000002")
	catalog := config.AgentCatalog.(*fakes.AgentCatalog)
	catalog.Set(owner, spare)
	exec, calls := fakes.ControlledExecutor()
	first := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, first)
	originalTask := fixtures.Assign(t, first, op.ID, "inspect")
	original := fixtures.Receive(t, calls)
	if original.Agent != owner.ID {
		t.Fatalf("unexpected initial Agent: %+v", original)
	}
	queued := fixtures.Assign(t, first, op.ID, "discover")
	assertAffinityTaskQueued(t, first, op.ID, 1, calls)
	fixtures.CloseRunner(t, first)
	config.RuleSelector = fakes.RuleSelectorFunc(func(context.Context, runner.RuleSelection) (missions.RuleSet, error) {
		selections.Add(1)
		return nil, errors.New("must not reselect affinity for a saved operation")
	})

	owner.Stale = true
	catalog.Set(owner, spare)
	observed := make(chan fakes.Invocation, 1)
	exec.Recover = func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		select {
		case observed <- fakes.Invocation{Agent: agent, Call: call}:
			return fixtures.Output(call.ActionName), nil
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
	}
	restarted := fixtures.NewRunner(t, config, exec)
	recovered := fixtures.Receive(t, observed)
	if recovered.Agent != original.Agent || recovered.Call.Work != original.Call.Work {
		t.Fatalf("recovery changed the saved Dispatch: %+v", recovered)
	}
	offline := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool {
		return op.Tasks[0].Status == operations.TaskSucceeded && op.Preflight != nil && !op.Preflight.Ready
	})
	assertAffinityTaskQueued(t, restarted, op.ID, 1, calls)

	// A live owner with an unavailable action still cannot delegate its files to
	// the ready spare. Wait for the saved preflight change to observe rediscovery.
	owner.Stale = false
	owner.Actions[1].Status = actions.Unavailable
	owner.Actions[1].MissingRequirements = []actions.Requirement{{Kind: actions.ExecutableRequirement, Name: "git"}}
	catalog.Set(owner, spare)
	fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool {
		return op.Preflight.Version > offline.Preflight.Version && !op.Preflight.Ready
	})
	assertAffinityTaskQueued(t, restarted, op.ID, 1, calls)

	owner.Actions[1].Status = actions.Ready
	owner.Actions[1].MissingRequirements = nil
	catalog.Set(owner, spare)
	resumed := fixtures.Receive(t, calls)
	if resumed.Agent != owner.ID || resumed.Call.Work.TaskID != queued.ID {
		t.Fatalf("returning owner did not resume its queued task: %+v", resumed)
	}
	resumed.Reply <- fixtures.Output("discover")
	settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool {
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
		len(config.MissionStore.(*fakes.MissionStore).Snapshot().Rules) != 0 {
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

func assertAffinityTaskQueued(t *testing.T, r *runner.Runner, operationID string, index int, calls <-chan fakes.Invocation) operations.Operation {
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
		t.Fatalf("waiting task dispatched to another Agent: %+v", call)
	default:
	}
	return op
}
