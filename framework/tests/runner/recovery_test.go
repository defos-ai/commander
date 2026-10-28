package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestRecoveryObservesSavedResultWithoutNewExecution(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "running at shutdown"
		if interrupted {
			name = "interrupted after lost result receipt"
		}
		t.Run(name, func(t *testing.T) {
			config := fixtures.Config()
			started := make(chan fakes.Invocation, 1)
			var dispatches atomic.Int32
			e := fakes.Executor{Run: func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
				dispatches.Add(1)
				started <- fakes.Invocation{Agent: agent, Call: call}
				if interrupted {
					return actions.ActionOutput{}, executions.ErrIndeterminate
				}
				<-ctx.Done()
				return actions.ActionOutput{}, ctx.Err()
			}}
			first := fixtures.NewRunner(t, config, e)
			op := fixtures.Start(t, first)
			task := fixtures.Assign(t, first, op.ID, "inspect")
			original := fixtures.Receive(t, started)
			if interrupted {
				fixtures.AwaitOperation(t, first, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskInterrupted })
			}
			fixtures.CloseRunner(t, first)
			observed := make(chan fakes.Invocation, 1)
			e.Recover = func(_ context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
				observed <- fakes.Invocation{Agent: agent, Call: call}
				return fixtures.Output(call.ActionName), nil
			}

			config.AgentCatalog.(*fakes.AgentCatalog).Set(fixtures.ReadyAgent("review-agent-00000002"))
			restarted := fixtures.NewRunner(t, config, e)
			recovered := fixtures.Receive(t, observed)
			settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })

			if dispatches.Load() != 1 || recovered.Agent != original.Agent || recovered.Call.Work != original.Call.Work {
				t.Fatalf("recovery repeated or changed work: dispatches=%d, recovered=%+v", dispatches.Load(), recovered)
			}
			got := settled.Tasks[0]
			if len(settled.Tasks) != 1 || got.ID != task.ID || got.CommandID != task.ID || got.Result == nil || got.Error != "" {
				t.Fatalf("recovery lost successful outcome: %+v", got)
			}
			if settled.Status != operations.Planning || got.Continuation.DecisionBy != operations.DecisionModel || got.Continuation.Status != operations.ContinuationPending {
				t.Fatalf("recovery did not return control to the model: %+v", settled)
			}
		})
	}
}

func TestUncertainAssignmentWriteSuspendsUntilRecovery(t *testing.T) {
	config := fixtures.Config()
	failure := errors.New("directory sync failed")
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, AfterQueue: func(operations.Operation) error {
		return errors.Join(operations.ErrWriteUncertain, failure)
	}}
	exec, calls := fakes.ControlledExecutor()
	first, err := runner.NewRunner(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := fixtures.Start(t, first)
	command := commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)}
	_, assignErr := first.Assign(t.Context(), command)
	stored, err := first.Get(t.Context(), op.ID)
	if err != nil || len(stored.Tasks) != 1 || stored.Tasks[0].Status != operations.TaskQueued {
		t.Fatalf("uncertain assignment lost the queued task: %+v, %v", stored, err)
	}
	saved := stored.Tasks[0]
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, waitErr := first.WaitTask(ctx, op.ID, saved.ID)
	_, eventErr := first.Events(ctx, op.ID, 0, false)
	_, retryErr := first.Assign(ctx, command)
	closeErr := first.Close(ctx)
	for _, err := range []error{assignErr, waitErr, eventErr, retryErr, closeErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
			t.Fatalf("uncertain assignment was hidden: %v", err)
		}
	}
	select {
	case call := <-calls:
		t.Fatalf("assignment dispatched before durability was confirmed: %+v", call)
	default:
	}

	restarted := fixtures.NewRunner(t, config, exec)
	call := fixtures.Receive(t, calls)
	if call.Agent != fixtures.AgentID || saved.AgentID != "" || call.Call.Work.TaskID != saved.ID || call.Call.Work.OperationID != op.ID || call.Call.ActionName != saved.ActionName || !reflect.DeepEqual(call.Call.Input, saved.Input) {
		t.Fatalf("recovery changed the saved assignment: %+v", call)
	}
	call.Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	fixtures.CloseRunner(t, restarted)
	assigned, dispatched := 0, 0
	for _, event := range settled.Events {
		switch event.Type {
		case operations.TaskAssigned:
			assigned++
		case operations.TaskDispatched:
			dispatched++
		}
	}
	if len(settled.Tasks) != 1 || settled.Tasks[0].ID != saved.ID || settled.Tasks[0].CommandID != saved.CommandID || assigned != 1 || dispatched != 1 {
		t.Fatalf("recovery duplicated the saved assignment: %+v", settled)
	}
	select {
	case call := <-calls:
		t.Fatalf("recovery dispatched the assignment twice: %+v", call)
	default:
	}
}

func TestUncertainDispatchWriteSuspendsUntilRecovery(t *testing.T) {
	config := fixtures.Config()
	failure := errors.New("directory sync failed")
	var failed atomic.Bool
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, AfterDispatch: func(op operations.Operation) error {
		if op.Events[len(op.Events)-1].Type == operations.TaskDispatched && failed.CompareAndSwap(false, true) {
			return errors.Join(operations.ErrWriteUncertain, failure)
		}
		return nil
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
	task := fixtures.Assign(t, first, op.ID, "inspect")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, waitErr := first.WaitTask(ctx, op.ID, task.ID)
	_, eventErr := first.Events(ctx, op.ID, 0, false)
	_, assignErr := first.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect"})
	closeErr := first.Close(ctx)

	for _, err := range []error{waitErr, eventErr, assignErr, closeErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
			t.Fatalf("uncertain write was hidden: %v", err)
		}
	}
	select {
	case call := <-calls:
		t.Fatalf("dispatched before durability was confirmed: %+v", call)
	default:
	}
	stored, err := first.Get(ctx, op.ID)
	if err != nil || len(stored.Tasks) != 1 || stored.Tasks[0].Status != operations.TaskRunning {
		t.Fatalf("uncertain write lost the saved reservation: %+v, %v", stored, err)
	}

	restarted, err := runner.NewRunner(t.Context(), fakes.DurableExecutor{Executor: exec}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixtures.CloseRunner(t, restarted) })
	recovered := fixtures.Receive(t, calls)
	if recovered.Agent != fixtures.AgentID || recovered.Call.Work.TaskID != task.ID || recovered.Call.Work.OperationID != op.ID {
		t.Fatalf("recovery changed the reserved work: %+v", recovered)
	}
	recovered.Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	fixtures.CloseRunner(t, restarted)

	dispatchEvents := 0
	for _, event := range settled.Events {
		if event.Type == operations.TaskDispatched {
			dispatchEvents++
		}
	}
	if len(settled.Tasks) != 1 || settled.Tasks[0].CommandID != task.CommandID || dispatchEvents != 1 {
		t.Fatalf("recovery duplicated the reservation: %+v", settled)
	}
	select {
	case call := <-calls:
		t.Fatalf("recovery dispatched twice: %+v", call)
	default:
	}
}

func TestUncertainDispatchWithoutCommitSuspendsUntilRecovery(t *testing.T) {
	config := fixtures.Config()
	durable := config.OperationStore
	var failed atomic.Bool
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: durable, Dispatch: func(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
		if !failed.CompareAndSwap(false, true) {
			return durable.DispatchTask(ctx, operationID, dispatch)
		}
		attempted, err := durable.Get(ctx, operationID)
		if err != nil {
			return operations.Operation{}, err
		}
		if err := attempted.DispatchTask(dispatch); err != nil {
			return operations.Operation{}, err
		}
		return attempted, operations.ErrWriteUncertain
	}}
	exec, calls := fakes.ControlledExecutor()
	first, err := runner.NewRunner(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := fixtures.Start(t, first)
	task := fixtures.Assign(t, first, op.ID, "inspect")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, waitErr := first.WaitTask(ctx, op.ID, task.ID)
	_, assignErr := first.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect"})
	closeErr := first.Close(ctx)

	for _, err := range []error{waitErr, assignErr, closeErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) {
			t.Fatalf("uncertain dispatch did not pause the operation: %v", err)
		}
	}
	select {
	case call := <-calls:
		t.Fatalf("attempted dispatch executed without a committed reservation: %+v", call)
	default:
	}
	stored, err := first.Get(ctx, op.ID)
	if err != nil || len(stored.Tasks) != 1 || !reflect.DeepEqual(stored.Tasks[0], task) {
		t.Fatalf("attempted dispatch changed the durable queued task: %+v, %v", stored, err)
	}
	for _, event := range stored.Events {
		if event.Type == operations.TaskDispatched {
			t.Fatal("uncommitted dispatch appeared in durable events")
		}
	}

	restarted := fixtures.NewRunner(t, config, exec)
	recovered := fixtures.Receive(t, calls)
	if recovered.Agent != fixtures.AgentID || task.AgentID != "" || recovered.Call.Work.TaskID != task.ID || recovered.Call.Work.OperationID != op.ID {
		t.Fatalf("recovery changed the queued assignment: %+v", recovered)
	}
	recovered.Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	fixtures.CloseRunner(t, restarted)
	dispatchEvents := 0
	for _, event := range settled.Events {
		if event.Type == operations.TaskDispatched {
			dispatchEvents++
		}
	}
	if len(settled.Tasks) != 1 || settled.Tasks[0].ID != task.ID || settled.Tasks[0].CommandID != task.CommandID || dispatchEvents != 1 {
		t.Fatalf("recovery duplicated the queued assignment: %+v", settled)
	}
	select {
	case call := <-calls:
		t.Fatalf("recovery dispatched the queued assignment twice: %+v", call)
	default:
	}
}

func TestUncertainResultWritePreservesAutomaticChildrenUntilRecovery(t *testing.T) {
	config := fixtures.Config()
	config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
		m.Rules = missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{"discover"}},
		}
	})
	failure := errors.New("directory sync failed")
	var failed atomic.Bool
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, AfterFinish: func(operations.Operation) error {
		if failed.CompareAndSwap(false, true) {
			return errors.Join(operations.ErrWriteUncertain, failure)
		}
		return nil
	}}
	exec, calls := fakes.ControlledExecutor()
	var recoveries atomic.Int32
	exec.Recover = func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error) {
		recoveries.Add(1)
		return actions.ActionOutput{}, errors.New("unexpected recovery of completed work")
	}
	first, err := runner.NewRunner(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := fixtures.Start(t, first)
	parent := fixtures.Assign(t, first, op.ID, "discover")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("discover",
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
		actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, waitErr := first.WaitTask(ctx, op.ID, parent.ID)
	_, eventErr := first.Events(ctx, op.ID, 0, false)
	for _, err := range []error{waitErr, eventErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
			t.Fatalf("uncertain result persistence was hidden: %v", err)
		}
	}
	select {
	case call := <-calls:
		t.Fatalf("automatic child dispatched before durability was confirmed: %+v", call)
	case <-time.After(150 * time.Millisecond):
	}
	if err := first.Close(ctx); !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
		t.Fatalf("shutdown hid uncertain result persistence: %v", err)
	}
	stored, err := first.Get(ctx, op.ID)
	if err != nil || len(stored.Tasks) != 3 {
		t.Fatalf("uncertain write lost the result or automatic children: %+v, %v", stored, err)
	}
	savedParent := stored.Tasks[0]
	if savedParent.ID != parent.ID || savedParent.CommandID != parent.CommandID || savedParent.Status != operations.TaskSucceeded || savedParent.Result == nil || savedParent.Continuation == nil || len(savedParent.Continuation.CreatedTaskIDs) != 2 {
		t.Fatalf("uncertain write lost the completed parent: %+v", savedParent)
	}
	savedChildren := make(map[string]operations.Task)
	for _, child := range stored.Tasks[1:] {
		if child.Status != operations.TaskQueued || child.ParentTaskID != parent.ID || child.CommandID != child.ID {
			t.Fatalf("uncertain result did not preserve a queued child: %+v", child)
		}
		savedChildren[child.ID] = child
	}

	restarted := fixtures.NewRunner(t, config, exec)
	for range 2 {
		call := fixtures.Receive(t, calls)
		child, exists := savedChildren[call.Call.Work.TaskID]
		if !exists || call.Call.Work.OperationID != op.ID || call.Agent != fixtures.AgentID || child.AgentID != "" || call.Call.ActionName != child.ActionName || !reflect.DeepEqual(call.Call.Input, child.Input) {
			t.Fatalf("recovery repeated the parent or changed an automatic child: %+v", call)
		}
		delete(savedChildren, child.ID)
		call.Reply <- fixtures.Output("inspect")
	}
	settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	fixtures.CloseRunner(t, restarted)
	if len(settled.Tasks) != 3 || recoveries.Load() != 0 {
		t.Fatalf("recovery duplicated or lost saved work: recoveries=%d, operation=%+v", recoveries.Load(), settled)
	}
	gotParent := settled.Tasks[0]
	if gotParent.CommandID != savedParent.CommandID || !reflect.DeepEqual(gotParent.FinishedAt, savedParent.FinishedAt) || !reflect.DeepEqual(gotParent.Result, savedParent.Result) {
		t.Fatalf("recovery changed the saved parent completion: %+v", gotParent)
	}
	if gotParent.Continuation == nil || !reflect.DeepEqual(gotParent.Continuation.CreatedTaskIDs, savedParent.Continuation.CreatedTaskIDs) {
		t.Fatalf("recovery replaced the saved automatic children: %+v", gotParent.Continuation)
	}
	finished := 0
	for _, event := range settled.Events {
		if event.Type == operations.TaskFinished && event.TaskID == parent.ID {
			finished++
		}
	}
	if finished != 1 {
		t.Fatalf("parent completion was recorded %d times", finished)
	}
	select {
	case call := <-calls:
		t.Fatalf("recovery duplicated automatic work: %+v", call)
	default:
	}
}

func TestUncertainResolutionWritePreservesChildrenUntilRecovery(t *testing.T) {
	config := fixtures.Config()
	failure := errors.New("directory sync failed")
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, AfterResolve: func(operations.Operation) error {
		return errors.Join(operations.ErrWriteUncertain, failure)
	}}
	exec, calls := fakes.ControlledExecutor()
	var recoveries atomic.Int32
	exec.Recover = func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error) {
		recoveries.Add(1)
		return actions.ActionOutput{}, errors.New("unexpected recovery of completed work")
	}
	first, err := runner.NewRunner(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := fixtures.Start(t, first)
	parent := fixtures.Assign(t, first, op.ID, "discover")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("discover")
	before := fixtures.AwaitOperation(t, first, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	continuation := before.Tasks[0].Continuation
	if continuation == nil || continuation.Status != operations.ContinuationPending || continuation.DecisionBy != operations.DecisionModel {
		t.Fatalf("parent is not awaiting a model decision: %+v", before.Tasks[0])
	}
	command := commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: parent.ID, Version: continuation.Version,
		Actions: []actions.NextAction{
			{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
			{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)},
		}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, resolveErr := first.Resolve(ctx, command)
	_, eventErr := first.Events(ctx, op.ID, 0, false)
	_, assignErr := first.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect"})
	_, retryErr := first.Resolve(ctx, command)
	closeErr := first.Close(ctx)
	for _, err := range []error{resolveErr, eventErr, assignErr, retryErr, closeErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
			t.Fatalf("uncertain resolution persistence was hidden: %v", err)
		}
	}
	select {
	case call := <-calls:
		t.Fatalf("resolved child dispatched before durability was confirmed: %+v", call)
	default:
	}
	stored, err := first.Get(ctx, op.ID)
	if err != nil || len(stored.Tasks) != 3 {
		t.Fatalf("uncertain resolution lost its children: %+v, %v", stored, err)
	}
	savedParent := stored.Tasks[0]
	if savedParent.Continuation.Status != operations.ContinuationResolved || savedParent.Continuation.Version != continuation.Version+1 || len(savedParent.Continuation.CreatedTaskIDs) != 2 {
		t.Fatalf("uncertain resolution lost its decision: %+v", savedParent.Continuation)
	}
	savedEvent := stored.Events[len(stored.Events)-1]
	if len(stored.Events) != len(before.Events)+len(command.Actions)+1 || savedEvent.Type != operations.ContinuationResolvedEvent || savedEvent.TaskID != parent.ID {
		t.Fatalf("uncertain resolution lost its event: %+v", stored.Events)
	}
	savedChildren := make(map[string]operations.Task)
	for i, child := range stored.Tasks[1:] {
		if child.Status != operations.TaskQueued || child.ParentTaskID != parent.ID || child.CommandID != child.ID || child.ID != savedParent.Continuation.CreatedTaskIDs[i] {
			t.Fatalf("uncertain resolution did not preserve a queued child: %+v", child)
		}
		savedChildren[child.ID] = child
	}

	restarted := fixtures.NewRunner(t, config, exec)
	for range 2 {
		call := fixtures.Receive(t, calls)
		child, exists := savedChildren[call.Call.Work.TaskID]
		if !exists || call.Call.Work.OperationID != op.ID || call.Agent != fixtures.AgentID || child.AgentID != "" || call.Call.ActionName != child.ActionName || !reflect.DeepEqual(call.Call.Input, child.Input) {
			t.Fatalf("recovery repeated the parent or changed a resolved child: %+v", call)
		}
		delete(savedChildren, child.ID)
		call.Reply <- fixtures.Output("inspect")
	}
	settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	if _, err := restarted.Resolve(ctx, command); err == nil || errors.Is(err, operations.ErrWriteUncertain) {
		t.Fatalf("recovery did not reject the stale decision: %v", err)
	}
	fixtures.CloseRunner(t, restarted)
	if len(settled.Tasks) != 3 || recoveries.Load() != 0 || !reflect.DeepEqual(settled.Tasks[0], savedParent) {
		t.Fatalf("recovery changed saved work: recoveries=%d, operation=%+v", recoveries.Load(), settled)
	}
	resolved := 0
	for _, event := range settled.Events {
		if event.Type == operations.ContinuationResolvedEvent && event.TaskID == parent.ID {
			resolved++
			if !reflect.DeepEqual(event, savedEvent) {
				t.Fatalf("recovery changed the saved decision event: %+v", event)
			}
		}
	}
	if resolved != 1 {
		t.Fatalf("model decision was recorded %d times", resolved)
	}
	select {
	case call := <-calls:
		t.Fatalf("recovery dispatched resolved work twice: %+v", call)
	default:
	}
}

func TestUncertainCreateRetainsOperationForRecovery(t *testing.T) {
	config := fixtures.Config()
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, CreateError: operations.ErrWriteUncertain}
	exec, calls := fakes.ControlledExecutor()
	first, err := runner.NewRunner(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	opID := id.OperationID()

	op, startErr := first.Start(t.Context(), commands.StartOperationCommand{OperationID: opID, MissionName: "review", Input: []byte(`{}`)})
	_, assignErr := first.Assign(t.Context(), commands.AssignTaskCommand{OperationID: opID, ActionName: "inspect"})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	closeErr := first.Close(ctx)

	if op.ID != opID {
		t.Fatalf("uncertain create lost its operation ID: %+v", op)
	}
	for _, err := range []error{startErr, assignErr, closeErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) {
			t.Fatalf("uncertain creation was hidden: %v", err)
		}
	}
	restarted := fixtures.NewRunner(t, config, exec)
	task := fixtures.Assign(t, restarted, opID, "inspect")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, restarted, opID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	all, err := restarted.List(t.Context())
	if err != nil || len(all) != 1 || len(settled.Tasks) != 1 || settled.Tasks[0].ID != task.ID {
		t.Fatalf("recovery duplicated work: %+v, %v", all, err)
	}
}
