package runner_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestCancellationWriteFailurePreservesQueuedWorkAndRetry(t *testing.T) {
	config := fixtures.Config()
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	fixtures.Assign(t, r, op.ID, "inspect")
	first := fixtures.Receive(t, calls)
	second := fixtures.Assign(t, r, op.ID, "inspect")
	third := fixtures.Assign(t, r, op.ID, "inspect")
	before, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("database unavailable")
	config.OperationStore.(*fakes.OperationStore).FailNextUpdate(failure)
	command := commands.CancelOperationCommand{OperationID: op.ID}

	_, cancelErr := r.Cancel(t.Context(), command)
	stored, err := r.Get(t.Context(), op.ID)
	if !errors.Is(cancelErr, failure) || err != nil || !reflect.DeepEqual(stored, before) {
		t.Fatalf("failed cancellation changed durable work: operation=%+v, cancel=%v, get=%v", stored, cancelErr, err)
	}
	events, err := r.Events(t.Context(), op.ID, 0, false)
	if err != nil || !reflect.DeepEqual(events, before.Events) {
		t.Fatalf("failed cancellation changed runner events: %+v, %v", events, err)
	}

	// The failed cancellation must leave queued work eligible to execute.
	first.Reply <- fixtures.Output("inspect")
	next := fixtures.Receive(t, calls)
	if next.Call.Work.TaskID != second.ID {
		t.Fatalf("failed cancellation lost the next queued task: %+v", next)
	}
	cancelled, err := r.Cancel(t.Context(), command)
	if err != nil || cancelled.Status != operations.Cancelled || cancelled.Tasks[1].Status != operations.TaskRunning || cancelled.Tasks[2].ID != third.ID || cancelled.Tasks[2].Status != operations.TaskCancelled {
		t.Fatalf("cancellation retry did not commit the remaining queue: %+v, %v", cancelled, err)
	}
	if _, err := r.Cancel(t.Context(), command); !errors.Is(err, operations.ErrFinished) {
		t.Fatalf("repeated cancellation should reject the finished operation: %v", err)
	}
	next.Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[1].Status == operations.TaskSucceeded })
	fixtures.CloseRunner(t, r)
	cancellations := 0
	for _, event := range settled.Events {
		if event.Type == operations.OperationCancelled {
			cancellations++
		}
	}
	if cancellations != 1 || settled.Status != operations.Cancelled || settled.Tasks[2].Status != operations.TaskCancelled {
		t.Fatalf("retry lost or duplicated cancellation: %+v", settled)
	}
	select {
	case call := <-calls:
		t.Fatalf("cancelled queued task dispatched: %+v", call)
	default:
	}
}

func TestUncertainCancellationRecoversRunningResultsWithoutNewWork(t *testing.T) {
	config := fixtures.Config()
	config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
		m.Rules = missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{"discover"}},
		}
	})
	failure := errors.New("directory sync failed")
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, AfterCancel: func(operations.Operation) error {
		return errors.Join(operations.ErrWriteUncertain, failure)
	}}
	exec, calls := fakes.ControlledExecutor()
	first, err := runner.NewRunner(t.Context(), fakes.DurableExecutor{Executor: exec}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := fixtures.Start(t, first)
	running := fixtures.Assign(t, first, op.ID, "discover")
	original := fixtures.Receive(t, calls)
	queued := fixtures.Assign(t, first, op.ID, "inspect")
	command := commands.CancelOperationCommand{OperationID: op.ID}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, cancelErr := first.Cancel(ctx, command)
	_, waitErr := first.WaitTask(ctx, op.ID, running.ID)
	_, eventErr := first.Events(ctx, op.ID, 0, false)
	_, retryErr := first.Cancel(ctx, command)
	_, assignErr := first.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect"})
	closeErr := first.Close(ctx)
	for _, err := range []error{cancelErr, waitErr, eventErr, retryErr, assignErr, closeErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
			t.Fatalf("uncertain cancellation was hidden: %v", err)
		}
	}
	stored, err := first.Get(ctx, op.ID)
	if err != nil || stored.Status != operations.Cancelled || len(stored.Tasks) != 2 || stored.Tasks[0].Status != operations.TaskRunning || stored.Tasks[1].ID != queued.ID || stored.Tasks[1].Status != operations.TaskCancelled {
		t.Fatalf("uncertain cancellation lost its visible snapshot: %+v, %v", stored, err)
	}
	select {
	case call := <-calls:
		t.Fatalf("uncertain cancellation dispatched queued work: %+v", call)
	default:
	}

	savedResult := fixtures.Output("discover", actions.NextAction{ActionName: "inspect"})
	observed := make(chan fakes.Invocation, 2)
	exec.Recover = func(_ context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		observed <- fakes.Invocation{Agent: agent, Call: call}
		return savedResult, nil
	}
	restarted, err := runner.NewRunner(t.Context(), fakes.DurableExecutor{Executor: exec}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixtures.CloseRunner(t, restarted) })
	recovered := fixtures.Receive(t, observed)
	if recovered.Agent != original.Agent || !reflect.DeepEqual(recovered.Call, original.Call) {
		t.Fatalf("cancellation recovery changed the dispatched command: %+v", recovered)
	}
	settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	fixtures.CloseRunner(t, restarted)
	if settled.Status != operations.Cancelled || len(settled.Tasks) != 2 || settled.Tasks[0].ID != running.ID || !reflect.DeepEqual(settled.Tasks[0].Result, &savedResult) || !reflect.DeepEqual(settled.Tasks[1], stored.Tasks[1]) {
		t.Fatalf("recovery lost cancellation or created follow-up work: %+v", settled)
	}
	cancellations := 0
	for _, event := range settled.Events {
		if event.Type == operations.OperationCancelled {
			cancellations++
		}
	}
	if cancellations != 1 {
		t.Fatalf("recovery recorded cancellation %d times", cancellations)
	}
	select {
	case call := <-calls:
		t.Fatalf("recovery dispatched cancelled or automatic work: %+v", call)
	case call := <-observed:
		t.Fatalf("recovery observed more than the running task: %+v", call)
	default:
	}
}
