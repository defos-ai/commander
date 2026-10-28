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

func TestWaitingClientsDoNotBlockSchedulingOrCancellation(t *testing.T) {
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, fixtures.Config(), exec)
	first := fixtures.Start(t, r)
	fixtures.Assign(t, r, first.ID, "inspect")
	firstCall := fixtures.Receive(t, calls)
	second := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, second.ID, "inspect")
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
	firstCall.Reply <- fixtures.Output("inspect")
	secondCall := fixtures.Receive(t, calls)
	if _, err := r.Cancel(ctx, commands.CancelOperationCommand{OperationID: second.ID}); err != nil {
		t.Fatal(err)
	}
	secondCall.Reply <- fixtures.Output("inspect", actions.NextAction{ActionName: "follow-up"})

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
	exec := fakes.Executor{Run: func(ctx context.Context, _ string, call actions.ActionCall) (actions.ActionOutput, error) {
		close(started)
		<-ctx.Done()
		<-release
		return fixtures.Output(call.ActionName), nil
	}}
	r := fixtures.NewRunner(t, fixtures.Config(), exec)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	op := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, op.ID, "inspect")
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
	config := fixtures.Config()
	config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
		m.Rules = missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{"discover"}},
		}
	})
	exec, calls := fakes.ControlledExecutor()
	r, err := runner.NewRunner(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Close(ctx)
	})
	op := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, op.ID, "discover")
	call := fixtures.Receive(t, calls)
	savedResult := fixtures.Output("discover", actions.NextAction{ActionName: "inspect"})
	failure := errors.New("database unavailable")
	store := config.OperationStore.(*fakes.OperationStore)
	store.FailNextUpdate(failure)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	call.Reply <- savedResult
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
	observed := make(chan fakes.Invocation, 2)
	exec.Recover = func(_ context.Context, agent string, recovered actions.ActionCall) (actions.ActionOutput, error) {
		observed <- fakes.Invocation{Agent: agent, Call: recovered}
		return savedResult, nil
	}
	restarted := fixtures.NewRunner(t, config, exec)
	recovered := fixtures.Receive(t, observed)
	if recovered.Agent != call.Agent || recovered.Call.Work != call.Call.Work {
		t.Fatalf("result reconciliation changed the saved command: %+v", recovered)
	}
	child := fixtures.Receive(t, calls)
	if child.Call.Work.TaskID == task.ID || child.Call.Work.OperationID != op.ID || child.Call.ActionName != "inspect" {
		t.Fatalf("recovery repeated the parent instead of starting its child: %+v", child)
	}
	child.Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	fixtures.CloseRunner(t, restarted)
	if len(settled.Tasks) != 2 {
		t.Fatalf("recovery duplicated or lost automatic work: %+v", settled)
	}
	parent := settled.Tasks[0]
	if parent.ID != task.ID || parent.CommandID != task.CommandID || !reflect.DeepEqual(parent.Result, &savedResult) {
		t.Fatalf("recovery changed the parent command or result: %+v", parent)
	}
	c := parent.Continuation
	if settled.Tasks[1].ID != child.Call.Work.TaskID || settled.Tasks[1].ParentTaskID != task.ID || c == nil || len(c.CreatedTaskIDs) != 1 || c.CreatedTaskIDs[0] != settled.Tasks[1].ID {
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
