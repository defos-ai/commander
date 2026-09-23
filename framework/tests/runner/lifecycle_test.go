package runner_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"commander/framework/comms/clients/controller/commands"
	"commander/framework/controller/runner"
	"commander/framework/domain/actions"
	"commander/framework/domain/operations"
)

func TestWaitingClientsDoNotBlockSchedulingOrCancellation(t *testing.T) {
	exec, calls := controlledExecutor()
	r := newRunner(t, fixture(), exec)
	first := start(t, r)
	assign(t, r, first.ID, "inspect")
	firstCall := receive(t, calls)
	second := start(t, r)
	task := assign(t, r, second.ID, "inspect")
	queued, err := r.Get(t.Context(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	waited, polled := make(chan error, 1), make(chan error, 1)
	go func() {
		got, err := r.WaitTask(ctx, second.ID, task.ID)
		if err == nil && got.Status != operations.TaskSucceeded {
			err = errors.New("waiting client lost the successful result")
		}
		waited <- err
	}()
	go func() {
		events, err := r.Events(ctx, second.ID, uint64(len(queued.Events)), true)
		if err == nil && len(events) == 0 {
			err = errors.New("long poll missed the dispatch event")
		}
		polled <- err
	}()

	select {
	case call := <-calls:
		t.Fatalf("agent capacity exceeded across operations: %+v", call)
	case <-time.After(150 * time.Millisecond):
	}
	firstCall.reply <- output("inspect")
	secondCall := receive(t, calls)
	if _, err := r.Cancel(ctx, commands.CancelOperationCommand{OperationID: second.ID}); err != nil {
		t.Fatal(err)
	}
	secondCall.reply <- output("inspect", actions.NextAction{ActionName: "follow-up", AgentID: agentID})

	for _, result := range []<-chan error{waited, polled} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Get(ctx, second.ID)
	if err != nil || got.Status != operations.Cancelled || len(got.Tasks) != 1 || got.Tasks[0].Result == nil {
		t.Fatalf("completion lost cancellation or its result: %+v, %v", got, err)
	}
}

func TestCloseWaitsForSuccessfulResultsAndCanBeRetried(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	exec := executor{run: func(ctx context.Context, _ string, call actions.ActionCall) (actions.ActionOutput, error) {
		close(started)
		<-ctx.Done()
		<-release
		return output(call.ActionName), nil
	}}
	r := newRunner(t, fixture(), exec)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	op := start(t, r)
	task := assign(t, r, op.ID, "inspect")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("task was not dispatched")
	}
	deadline, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := r.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close should time out while execution is returning: %v", err)
	}
	ctx, done := context.WithTimeout(t.Context(), 5*time.Second)
	defer done()
	closed := make(chan error, 2)
	for range 2 {
		go func() { closed <- r.Close(ctx) }()
	}
	close(release)

	for range 2 {
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.WaitTask(ctx, op.ID, task.ID)
	if err != nil || got.Status != operations.TaskSucceeded || got.Result == nil {
		t.Fatalf("shutdown dropped successful work: %+v, %v", got, err)
	}
	events, err := r.Events(ctx, op.ID, 0, true)
	if err != nil || len(events) == 0 || events[len(events)-1].Type != operations.TaskFinished {
		t.Fatalf("shutdown lost the final event: %+v, %v", events, err)
	}
}

func TestResultSaveFailureReachesWaitersAndClose(t *testing.T) {
	config := fixture()
	exec, calls := controlledExecutor()
	r, err := runner.NewRunner(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Close(ctx)
	})
	op := start(t, r)
	task := assign(t, r, op.ID, "inspect")
	call := receive(t, calls)
	failure := errors.New("database unavailable")
	store := config.OperationStore.(*operationStore)
	store.mu.Lock()
	store.failNext = failure
	store.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	call.reply <- output("inspect")
	_, waitErr := r.WaitTask(ctx, op.ID, task.ID)
	_, eventErr := r.Events(ctx, op.ID, 0, false)
	closeErr := r.Close(ctx)

	for _, err := range []error{waitErr, eventErr, closeErr} {
		if !errors.Is(err, failure) {
			t.Fatalf("result persistence failure was hidden: %v", err)
		}
	}
	stored, err := r.Get(ctx, op.ID)
	if err != nil || stored.Tasks[0].Status != operations.TaskRunning || stored.Tasks[0].Result != nil {
		t.Fatalf("failed write corrupted recoverable work: %+v, %v", stored, err)
	}
}
