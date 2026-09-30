package runner_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"commander/framework/comms/clients/controller/commands"
	"commander/framework/controller/runner"
	"commander/framework/domain/actions"
	"commander/framework/domain/missions"
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
	secondCall.reply <- output("inspect", actions.NextAction{ActionName: "follow-up"})

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

func TestResultSaveFailureReachesWaitersAndRecoversWithoutNewExecution(t *testing.T) {
	config := fixture()
	config.MissionStore.(*missionStore).mission.Automations = map[string]missions.Automation{
		"discover": {Run: []string{"inspect"}, Continue: missions.WhenAll},
	}
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
	task := assign(t, r, op.ID, "discover")
	call := receive(t, calls)
	savedResult := output("discover", actions.NextAction{ActionName: "inspect"})
	failure := errors.New("database unavailable")
	store := config.OperationStore.(*operationStore)
	store.failNextUpdate(failure)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	call.reply <- savedResult
	_, waitErr := r.WaitTask(ctx, op.ID, task.ID)
	_, eventErr := r.Events(ctx, op.ID, 0, false)
	closeErr := r.Close(ctx)

	for _, err := range []error{waitErr, eventErr, closeErr} {
		if !errors.Is(err, failure) {
			t.Fatalf("result persistence failure was hidden: %v", err)
		}
	}
	stored, err := r.Get(ctx, op.ID)
	if err != nil || len(stored.Tasks) != 1 || stored.Tasks[0].Status != operations.TaskRunning || stored.Tasks[0].Result != nil {
		t.Fatalf("failed write corrupted recoverable work: %+v, %v", stored, err)
	}
	observed := make(chan invocation, 2)
	exec.recover = func(_ context.Context, agent string, recovered actions.ActionCall) (actions.ActionOutput, error) {
		observed <- invocation{agent: agent, call: recovered}
		return savedResult, nil
	}
	restarted := newRunner(t, config, exec)
	recovered := receive(t, observed)
	if recovered.agent != call.agent || recovered.call.Work != call.call.Work {
		t.Fatalf("result reconciliation changed the saved command: %+v", recovered)
	}
	child := receive(t, calls)
	if child.call.Work.TaskID == task.ID || child.call.Work.OperationID != op.ID || child.call.ActionName != "inspect" {
		t.Fatalf("recovery repeated the parent instead of starting its child: %+v", child)
	}
	child.reply <- output("inspect")
	settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	closeRunner(t, restarted)
	if len(settled.Tasks) != 2 {
		t.Fatalf("recovery duplicated or lost automatic work: %+v", settled)
	}
	parent := settled.Tasks[0]
	if parent.ID != task.ID || parent.CommandID != task.CommandID || !reflect.DeepEqual(parent.Result, &savedResult) {
		t.Fatalf("recovery changed the parent command or result: %+v", parent)
	}
	c := parent.Continuation
	if settled.Tasks[1].ID != child.call.Work.TaskID || settled.Tasks[1].ParentTaskID != task.ID || c == nil || len(c.CreatedTaskIDs) != 1 || c.CreatedTaskIDs[0] != settled.Tasks[1].ID {
		t.Fatalf("recovery lost the automatic child relationship: %+v", settled)
	}
	finished := 0
	for _, event := range settled.Events {
		if event.Type == operations.TaskFinished && event.TaskID == task.ID {
			finished++
		}
	}
	if finished != 1 {
		t.Fatalf("reconciled parent completion was recorded %d times", finished)
	}
	select {
	case call := <-calls:
		t.Fatalf("recovery dispatched duplicate work: %+v", call)
	case call := <-observed:
		t.Fatalf("recovery observed the completed parent again: %+v", call)
	default:
	}
}
