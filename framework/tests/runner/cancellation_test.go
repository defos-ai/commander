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
)

func TestCancellationWriteFailurePreservesQueuedWorkAndRetry(t *testing.T) {
	config := fixture()
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	assign(t, r, op.ID, "inspect")
	first := receive(t, calls)
	second := assign(t, r, op.ID, "inspect")
	third := assign(t, r, op.ID, "inspect")
	before, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("database unavailable")
	config.OperationStore.(*operationStore).failNextUpdate(failure)
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
	first.reply <- output("inspect")
	next := receive(t, calls)
	if next.call.Work.TaskID != second.ID {
		t.Fatalf("failed cancellation lost the next queued task: %+v", next)
	}
	cancelled, err := r.Cancel(t.Context(), command)
	if err != nil || cancelled.Status != operations.Cancelled || cancelled.Tasks[1].Status != operations.TaskRunning || cancelled.Tasks[2].ID != third.ID || cancelled.Tasks[2].Status != operations.TaskCancelled {
		t.Fatalf("cancellation retry did not commit the remaining queue: %+v, %v", cancelled, err)
	}
	if _, err := r.Cancel(t.Context(), command); !errors.Is(err, operations.ErrFinished) {
		t.Fatalf("repeated cancellation should reject the finished operation: %v", err)
	}
	next.reply <- output("inspect")
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[1].Status == operations.TaskSucceeded })
	closeRunner(t, r)
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
	config := fixture()
	config.MissionStore.(*missionStore).mission.Rules = missions.RuleSet{
		missions.AggregateResults: {Actions: []string{"discover"}},
	}
	failure := errors.New("directory sync failed")
	config.OperationStore = &uncertainWriteStore{OperationStore: config.OperationStore, afterCancel: func(operations.Operation) error {
		return errors.Join(operations.ErrWriteUncertain, failure)
	}}
	exec, calls := controlledExecutor()
	first, err := runner.NewRunner(t.Context(), durableExecutor{exec}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := start(t, first)
	running := assign(t, first, op.ID, "discover")
	original := receive(t, calls)
	queued := assign(t, first, op.ID, "inspect")
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

	savedResult := output("discover", actions.NextAction{ActionName: "inspect"})
	observed := make(chan invocation, 2)
	exec.recover = func(_ context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		observed <- invocation{agent: agent, call: call}
		return savedResult, nil
	}
	restarted, err := runner.NewRunner(t.Context(), durableExecutor{exec}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(t, restarted) })
	recovered := receive(t, observed)
	if recovered.agent != original.agent || !reflect.DeepEqual(recovered.call, original.call) {
		t.Fatalf("cancellation recovery changed the dispatched command: %+v", recovered)
	}
	settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	closeRunner(t, restarted)
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
