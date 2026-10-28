package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestCustomAgentSelectorChoosesFromEligibleCandidates(t *testing.T) {
	config := fixtures.Config()
	first, preferred := fixtures.ReadyAgent(fixtures.AgentID), fixtures.ReadyAgent("review-agent-00000002")
	unavailable := fixtures.ReadyAgent("review-agent-00000003")
	unavailable.Stale = true
	config.AgentCatalog.(*fakes.AgentCatalog).Set(unavailable, preferred, first)
	selections := make(chan runner.AgentSelection, 4)
	config.AgentSelector = fakes.AgentSelectorFunc(func(_ context.Context, selection runner.AgentSelection) (string, error) {
		selections <- selection
		return preferred.ID, nil
	})
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, op.ID, "inspect")
	call := fixtures.Receive(t, calls)
	selection := <-selections

	if call.Agent != preferred.ID || call.Call.Work.TaskID != task.ID || selection.Task.ID != task.ID || selection.PreviousAgentID != "" {
		t.Fatalf("custom selection was not used: call=%+v, selection=%+v", call, selection)
	}
	if len(selection.Candidates) != 2 {
		t.Fatalf("selector received ineligible agents: %+v", selection.Candidates)
	}
	for _, candidate := range selection.Candidates {
		if (candidate.Agent.ID != first.ID && candidate.Agent.ID != preferred.ID) || candidate.RunningTasks != 0 || candidate.Capacity != 1 {
			t.Fatalf("unexpected candidate context: %+v", candidate)
		}
	}
	call.Reply <- fixtures.Output("inspect")
	fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
}

func TestAgentSelectorCannotMutateSnapshotsOrAddEligibleAgents(t *testing.T) {
	config := fixtures.Config()
	record := fixtures.ReadyAgent(fixtures.AgentID)
	record.Responsibilities = []string{"Inspect websites"}
	record.Actions[0].InputSchema = json.RawMessage(`{"type":"object"}`)
	catalog := fakes.NewSelectionCatalog(record)
	want := record.Clone()
	config.AgentCatalog = catalog
	var mutated, allowed atomic.Bool
	attempted := make(chan struct{}, 1)
	config.AgentSelector = fakes.AgentSelectorFunc(func(_ context.Context, selection runner.AgentSelection) (string, error) {
		if allowed.Load() {
			return fixtures.AgentID, nil
		}
		if mutated.CompareAndSwap(false, true) {
			selection.Task.Input[0] = '['
			selection.Candidates[0].Agent.ID = "review-agent-00000099"
			selection.Candidates[0].Agent.Responsibilities[0] = "altered"
			selection.Candidates[0].Agent.Actions[0].InputSchema[0] = '['
			attempted <- struct{}{}
			return selection.Candidates[0].Agent.ID, nil
		}
		return "", nil
	})
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, op.ID, "inspect")
	waitSelectionAttempt(t, attempted)
	assertSelectionDeferred(t, r, op.ID, task, calls)
	if !reflect.DeepEqual(catalog.Snapshot()[0], want) {
		t.Fatalf("selector modified the inventory snapshot: got=%+v, want=%+v", catalog.Snapshot()[0], want)
	}

	allowed.Store(true)
	call := fixtures.Receive(t, calls)
	if call.Agent != fixtures.AgentID || string(call.Call.Input) != "{}" {
		t.Fatalf("retry used selector mutations: %+v", call)
	}
	call.Reply <- fixtures.Output("inspect")
	fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
}

func TestAgentSelectionDefersWithoutFallbackAndRetries(t *testing.T) {
	for _, test := range []struct {
		name string
		id   string
		err  error
	}{
		{name: "no selection"},
		{name: "selection error", id: fixtures.AgentID, err: errors.New("decision service unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := fixtures.Config()
			var allowed atomic.Bool
			attempted := make(chan struct{}, 1)
			config.AgentSelector = fakes.AgentSelectorFunc(func(_ context.Context, selection runner.AgentSelection) (string, error) {
				if allowed.Load() {
					return selection.Candidates[0].Agent.ID, nil
				}
				select {
				case attempted <- struct{}{}:
				default:
				}
				return test.id, test.err
			})
			exec, calls := fakes.ControlledExecutor()
			r := fixtures.NewRunner(t, config, exec)
			op := fixtures.Start(t, r)
			task := fixtures.Assign(t, r, op.ID, "inspect")
			waitSelectionAttempt(t, attempted)
			assertSelectionDeferred(t, r, op.ID, task, calls)

			allowed.Store(true)
			call := fixtures.Receive(t, calls)
			if call.Call.Work.TaskID != task.ID || call.Agent != fixtures.AgentID {
				t.Fatalf("retry dispatched a different task or Agent: %+v", call)
			}
			call.Reply <- fixtures.Output("inspect")
			fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
		})
	}
}

func TestRoundRobinAdvancesOnlyAfterDurableDispatch(t *testing.T) {
	config := fixtures.Config()
	first, second := fixtures.ReadyAgent(fixtures.AgentID), fixtures.ReadyAgent("review-agent-00000002")
	config.AgentCatalog.(*fakes.AgentCatalog).Set(second, first)
	store := fakes.NewDispatchFailureStore(config.OperationStore)
	config.OperationStore = store
	previous := make(chan string, 16)
	config.AgentSelector = fakes.AgentSelectorFunc(func(ctx context.Context, selection runner.AgentSelection) (string, error) {
		select {
		case previous <- selection.PreviousAgentID:
		default:
		}
		return (runner.RoundRobinSelector{}).Select(ctx, selection)
	})
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	firstTask := fixtures.Assign(t, r, op.ID, "inspect")
	waitSelectionAttempt(t, store.Attempted())
	assertSelectionDeferred(t, r, op.ID, firstTask, calls)
	secondTask := fixtures.Assign(t, r, op.ID, "inspect")
	waitSelectionAttempt(t, store.Attempted())
	if _, err := r.Events(t.Context(), op.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	for len(previous) > 0 {
		if id := <-previous; id != "" {
			t.Fatalf("failed reservation advanced round-robin cursor to %q", id)
		}
	}

	store.Allow()
	firstCall, secondCall := fixtures.Receive(t, calls), fixtures.Receive(t, calls)
	assigned := map[string]string{firstCall.Call.Work.TaskID: firstCall.Agent, secondCall.Call.Work.TaskID: secondCall.Agent}
	if assigned[firstTask.ID] != first.ID || assigned[secondTask.ID] != second.ID {
		t.Fatalf("round-robin did not preserve reservation order: %+v", assigned)
	}
	var lastPrevious string
	for len(previous) > 0 {
		lastPrevious = <-previous
	}
	if lastPrevious != first.ID {
		t.Fatalf("successful reservation did not advance round-robin cursor: got %q, want %q", lastPrevious, first.ID)
	}
	firstCall.Reply <- fixtures.Output("inspect")
	secondCall.Reply <- fixtures.Output("inspect")
	fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return op.Tasks[0].Status == operations.TaskSucceeded && op.Tasks[1].Status == operations.TaskSucceeded
	})
	thirdTask := fixtures.Assign(t, r, op.ID, "inspect")
	thirdCall := fixtures.Receive(t, calls)
	if thirdCall.Call.Work.TaskID != thirdTask.ID || thirdCall.Agent != first.ID {
		t.Fatalf("round-robin did not wrap to the first available Agent: %+v", thirdCall)
	}
	select {
	case id := <-previous:
		if id != second.ID {
			t.Fatalf("wraparound lost last durable selection: got %q, want %q", id, second.ID)
		}
	default:
		t.Fatal("wraparound did not invoke the selector")
	}
	thirdCall.Reply <- fixtures.Output("inspect")
	fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[2].Status == operations.TaskSucceeded })
}

func TestClosingRunnerCancelsAgentSelection(t *testing.T) {
	config := fixtures.Config()
	attempted := make(chan struct{}, 1)
	config.AgentSelector = fakes.AgentSelectorFunc(func(ctx context.Context, selection runner.AgentSelection) (string, error) {
		attempted <- struct{}{}
		<-ctx.Done()
		return selection.Candidates[0].Agent.ID, nil
	})
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, op.ID, "inspect")
	waitSelectionAttempt(t, attempted)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatalf("closing runner did not cancel agent selection: %v", err)
	}
	stored, err := config.OperationStore.Get(t.Context(), op.ID)
	if err != nil || len(stored.Tasks) != 1 || !reflect.DeepEqual(stored.Tasks[0], task) {
		t.Fatalf("cancelled selection changed queued work: operation=%+v, error=%v", stored, err)
	}
	select {
	case call := <-calls:
		t.Fatalf("cancelled selection dispatched work: %+v", call)
	default:
	}
}

func assertSelectionDeferred(t *testing.T, r *runner.Runner, operationID string, task operations.Task, calls <-chan fakes.Invocation) {
	t.Helper()
	if _, err := r.Events(t.Context(), operationID, 0, false); err != nil {
		t.Fatal(err)
	}
	op, err := r.Get(t.Context(), operationID)
	if err != nil || len(op.Tasks) != 1 || !reflect.DeepEqual(op.Tasks[0], task) {
		t.Fatalf("selection failure changed queued work: operation=%+v, error=%v", op, err)
	}
	select {
	case call := <-calls:
		t.Fatalf("selection failure dispatched work: %+v", call)
	default:
	}
}

func waitSelectionAttempt(t *testing.T, attempted <-chan struct{}) {
	t.Helper()
	select {
	case <-attempted:
	case <-time.After(5 * time.Second):
		t.Fatal("expected agent selection attempt")
	}
}
