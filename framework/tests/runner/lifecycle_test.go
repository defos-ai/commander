package runner_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/workers/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestRunnerDefersRecoveredExecutionUntilRun(t *testing.T) {
	config := fixtures.Config()
	queued := fixtures.SeedMaintenanceTask(t, config, false)
	running := fixtures.SeedMaintenanceTask(t, config, true)
	exec, calls := fakes.ControlledExecutor()
	exec.Recover = exec.Run
	r, err := runner.New(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixtures.CloseRunner(t, r) })

	before, err := r.Get(t.Context(), running.ID)
	if err != nil || before.Tasks[0].Status != operations.TaskRunning || !reflect.DeepEqual(before.Tasks, running.Tasks) {
		t.Fatalf("construction lost saved work before Run: %+v, %v", before, err)
	}
	select {
	case call := <-calls:
		t.Fatalf("construction executed work before Run: %+v", call)
	case <-time.After(150 * time.Millisecond):
	}
	fixtures.StartRunner(t, r)
	recovered := fixtures.Receive(t, calls)
	if recovered.Call.Work.OperationID != running.ID || recovered.Call.Work.TaskID != running.Tasks[0].ID {
		t.Fatalf("Run did not resume the saved execution first: %+v", recovered)
	}
	recovered.Reply <- fixtures.Output("inspect")
	dispatched := fixtures.Receive(t, calls)
	if dispatched.Call.Work.OperationID != queued.ID || dispatched.Call.Work.TaskID != queued.Tasks[0].ID {
		t.Fatalf("Run did not preserve the saved queued task: %+v", dispatched)
	}
	dispatched.Reply <- fixtures.Output("inspect")
	for _, op := range []operations.Operation{running, queued} {
		settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
		if len(settled.Tasks) != 1 || settled.Tasks[0].ID != op.Tasks[0].ID || settled.Tasks[0].Result == nil {
			t.Fatalf("Run duplicated or lost recovered work: %+v", settled)
		}
	}
}

func TestRunnerCloseBeforeRunPreservesWorkAndRejectsStart(t *testing.T) {
	config := fixtures.Config()
	op := fixtures.SeedMaintenanceTask(t, config, false)
	exec, calls := fakes.ControlledExecutor()
	r, err := runner.New(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixtures.CloseRunner(t, r) })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	closed := make(chan error, 2)

	for range 2 {
		go func() { closed <- r.Close(ctx) }()
	}
	for range 2 {
		if err := awaitRunnerReturn(t, closed); err != nil {
			t.Fatalf("Close before Run waited for a nonexistent loop: %v", err)
		}
	}
	if err := r.Run(ctx); !errors.Is(err, runner.ErrClosed) {
		t.Fatalf("Run restarted a closed runner: %v", err)
	}
	_, assignErr := r.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect", Input: []byte(`{}`)})
	retained, readErr := r.Get(ctx, op.ID)

	if !errors.Is(assignErr, runner.ErrClosed) || readErr != nil || !reflect.DeepEqual(retained.Tasks, op.Tasks) {
		t.Fatalf("pre-start shutdown changed saved work or admitted a task: %+v, assign=%v, read=%v", retained, assignErr, readErr)
	}
	select {
	case call := <-calls:
		t.Fatalf("pre-start shutdown executed work: %+v", call)
	default:
	}
}

func TestRunnerRejectsDuplicateRunAndJoinsContextCancellation(t *testing.T) {
	exec, calls := fakes.ControlledExecutor()
	r, err := runner.New(t.Context(), exec, fixtures.Config())
	if err != nil {
		t.Fatal(err)
	}
	cancelRun, finished := fixtures.StartRunner(t, r)
	t.Cleanup(func() { fixtures.CloseRunner(t, r) })
	op := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, op.ID, "inspect")
	fixtures.Receive(t, calls)
	duplicate := make(chan error, 1)

	go func() { duplicate <- r.Run(t.Context()) }()

	if err := awaitRunnerReturn(t, duplicate); !errors.Is(err, runner.ErrAlreadyRunning) {
		t.Fatalf("duplicate Run did not retain one owner loop: %v", err)
	}
	waiter := make(chan error, 1)
	go func() { _, err := r.WaitTask(t.Context(), op.ID, task.ID); waiter <- err }()

	cancelRun()
	runErr, waitErr := awaitRunnerReturn(t, finished), awaitRunnerReturn(t, waiter)
	closeErr := r.Close(t.Context())
	retained, readErr := r.Get(t.Context(), op.ID)

	if runErr != nil || closeErr != nil || !errors.Is(waitErr, runner.ErrNotRunning) || readErr != nil {
		t.Fatalf("Run-context cancellation did not join work and release waiters: run=%v, close=%v, wait=%v, read=%v", runErr, closeErr, waitErr, readErr)
	}
	if retained.Tasks[0].Status != operations.TaskRunning || retained.Tasks[0].Result != nil {
		t.Fatalf("host cancellation replaced the recoverable remote outcome: %+v", retained.Tasks[0])
	}
	if err := r.Run(t.Context()); !errors.Is(err, runner.ErrClosed) {
		t.Fatalf("a cancelled Run restarted the runner: %v", err)
	}
}

func awaitRunnerReturn(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("runner lifecycle call did not return")
		return nil
	}
}

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
	r, err := runner.New(t.Context(), exec, fixtures.Config())
	if err != nil {
		t.Fatal(err)
	}
	_, finished := fixtures.StartRunner(t, r)
	t.Cleanup(func() { fixtures.CloseRunner(t, r) })
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
	if err := awaitRunnerReturn(t, finished); err != nil {
		t.Fatalf("Run returned before draining its successful result: %v", err)
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
		m.Policies = &policies.MissionPolicies{Continuation: &policies.ContinuationPolicy{
			AggregateResults: []string{"discover"},
		}}
	})
	exec, calls := fakes.ControlledExecutor()
	r, err := runner.New(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	fixtures.StartRunner(t, r)
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
