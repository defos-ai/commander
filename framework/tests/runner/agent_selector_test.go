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
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type agentSelectorFunc func(context.Context, runner.AgentSelection) (string, error)

func (agentSelectorFunc) Name() string { return "test-selector" }
func (f agentSelectorFunc) Select(ctx context.Context, selection runner.AgentSelection) (string, error) {
	return f(ctx, selection)
}

func TestCustomAgentSelectorChoosesFromEligibleCandidates(t *testing.T) {
	config := fixture()
	first, preferred := readyAgent(agentID), readyAgent("review-agent-00000002")
	unavailable := readyAgent("review-agent-00000003")
	unavailable.Stale = true
	config.AgentCatalog.(*agentCatalog).set(unavailable, preferred, first)
	selections := make(chan runner.AgentSelection, 4)
	config.AgentSelector = agentSelectorFunc(func(_ context.Context, selection runner.AgentSelection) (string, error) {
		selections <- selection
		return preferred.ID, nil
	})
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	task := assign(t, r, op.ID, "inspect")
	call := receive(t, calls)
	selection := <-selections

	if call.agent != preferred.ID || call.call.Work.TaskID != task.ID || selection.Task.ID != task.ID || selection.PreviousAgentID != "" {
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
	call.reply <- output("inspect")
	awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
}

func TestAgentSelectorCannotMutateSnapshotsOrAddEligibleAgents(t *testing.T) {
	config := fixture()
	record := readyAgent(agentID)
	record.Responsibilities = []string{"Inspect websites"}
	record.Actions[0].InputSchema = json.RawMessage(`{"type":"object"}`)
	catalog := &selectionCatalog{records: []agents.Agent{record}}
	want := record.Clone()
	config.AgentCatalog = catalog
	var mutated, allowed atomic.Bool
	attempted := make(chan struct{}, 1)
	config.AgentSelector = agentSelectorFunc(func(_ context.Context, selection runner.AgentSelection) (string, error) {
		if allowed.Load() {
			return agentID, nil
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
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	task := assign(t, r, op.ID, "inspect")
	waitSelectionAttempt(t, attempted)
	assertSelectionDeferred(t, r, op.ID, task, calls)
	if !reflect.DeepEqual(catalog.records[0], want) {
		t.Fatalf("selector modified the inventory snapshot: got=%+v, want=%+v", catalog.records[0], want)
	}

	allowed.Store(true)
	call := receive(t, calls)
	if call.agent != agentID || string(call.call.Input) != "{}" {
		t.Fatalf("retry used selector mutations: %+v", call)
	}
	call.reply <- output("inspect")
	awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
}

type selectionCatalog struct{ records []agents.Agent }

func (c *selectionCatalog) Agents(context.Context) ([]agents.Agent, error) { return c.records, nil }

func TestAgentSelectionDefersWithoutFallbackAndRetries(t *testing.T) {
	for _, test := range []struct {
		name string
		id   string
		err  error
	}{
		{name: "no selection"},
		{name: "selection error", id: agentID, err: errors.New("decision service unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := fixture()
			var allowed atomic.Bool
			attempted := make(chan struct{}, 1)
			config.AgentSelector = agentSelectorFunc(func(_ context.Context, selection runner.AgentSelection) (string, error) {
				if allowed.Load() {
					return selection.Candidates[0].Agent.ID, nil
				}
				select {
				case attempted <- struct{}{}:
				default:
				}
				return test.id, test.err
			})
			exec, calls := controlledExecutor()
			r := newRunner(t, config, exec)
			op := start(t, r)
			task := assign(t, r, op.ID, "inspect")
			waitSelectionAttempt(t, attempted)
			assertSelectionDeferred(t, r, op.ID, task, calls)

			allowed.Store(true)
			call := receive(t, calls)
			if call.call.Work.TaskID != task.ID || call.agent != agentID {
				t.Fatalf("retry dispatched a different task or agent: %+v", call)
			}
			call.reply <- output("inspect")
			awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
		})
	}
}

func TestRoundRobinAdvancesOnlyAfterDurableDispatch(t *testing.T) {
	config := fixture()
	first, second := readyAgent(agentID), readyAgent("review-agent-00000002")
	config.AgentCatalog.(*agentCatalog).set(second, first)
	store := &dispatchFailureStore{OperationStore: config.OperationStore, attempted: make(chan struct{}, 1)}
	config.OperationStore = store
	previous := make(chan string, 16)
	config.AgentSelector = agentSelectorFunc(func(ctx context.Context, selection runner.AgentSelection) (string, error) {
		select {
		case previous <- selection.PreviousAgentID:
		default:
		}
		return (runner.RoundRobinSelector{}).Select(ctx, selection)
	})
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	firstTask := assign(t, r, op.ID, "inspect")
	waitSelectionAttempt(t, store.attempted)
	assertSelectionDeferred(t, r, op.ID, firstTask, calls)
	secondTask := assign(t, r, op.ID, "inspect")
	waitSelectionAttempt(t, store.attempted)
	if _, err := r.Events(t.Context(), op.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	for len(previous) > 0 {
		if id := <-previous; id != "" {
			t.Fatalf("failed reservation advanced round-robin cursor to %q", id)
		}
	}

	store.allowed.Store(true)
	firstCall, secondCall := receive(t, calls), receive(t, calls)
	assigned := map[string]string{firstCall.call.Work.TaskID: firstCall.agent, secondCall.call.Work.TaskID: secondCall.agent}
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
	firstCall.reply <- output("inspect")
	secondCall.reply <- output("inspect")
	awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return op.Tasks[0].Status == operations.TaskSucceeded && op.Tasks[1].Status == operations.TaskSucceeded
	})
	thirdTask := assign(t, r, op.ID, "inspect")
	thirdCall := receive(t, calls)
	if thirdCall.call.Work.TaskID != thirdTask.ID || thirdCall.agent != first.ID {
		t.Fatalf("round-robin did not wrap to the first available agent: %+v", thirdCall)
	}
	select {
	case id := <-previous:
		if id != second.ID {
			t.Fatalf("wraparound lost last durable selection: got %q, want %q", id, second.ID)
		}
	default:
		t.Fatal("wraparound did not invoke the selector")
	}
	thirdCall.reply <- output("inspect")
	awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[2].Status == operations.TaskSucceeded })
}

func TestClosingRunnerCancelsAgentSelection(t *testing.T) {
	config := fixture()
	attempted := make(chan struct{}, 1)
	config.AgentSelector = agentSelectorFunc(func(ctx context.Context, selection runner.AgentSelection) (string, error) {
		attempted <- struct{}{}
		<-ctx.Done()
		return selection.Candidates[0].Agent.ID, nil
	})
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	task := assign(t, r, op.ID, "inspect")
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

func assertSelectionDeferred(t *testing.T, r *runner.Runner, operationID string, task operations.Task, calls <-chan invocation) {
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
