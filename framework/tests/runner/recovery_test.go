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
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestRecoveryObservesSavedResultWithoutNewExecution(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "running at shutdown"
		if interrupted {
			name = "interrupted after lost result receipt"
		}
		t.Run(name, func(t *testing.T) {
			config := fixture()
			started := make(chan invocation, 1)
			var dispatches atomic.Int32
			e := executor{run: func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
				dispatches.Add(1)
				started <- invocation{agent: agent, call: call}
				if interrupted {
					return actions.ActionOutput{}, executions.ErrIndeterminate
				}
				<-ctx.Done()
				return actions.ActionOutput{}, ctx.Err()
			}}
			first := newRunner(t, config, e)
			op := start(t, first)
			task := assign(t, first, op.ID, "inspect")
			original := receive(t, started)
			if interrupted {
				awaitOperation(t, first, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskInterrupted })
			}
			closeRunner(t, first)
			observed := make(chan invocation, 1)
			e.recover = func(_ context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
				observed <- invocation{agent: agent, call: call}
				return output(call.ActionName), nil
			}

			config.AgentCatalog.(*agentCatalog).set(readyAgent("review-agent-00000002"))
			restarted := newRunner(t, config, e)
			recovered := receive(t, observed)
			settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })

			if dispatches.Load() != 1 || recovered.agent != original.agent || recovered.call.Work != original.call.Work {
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
	config := fixture()
	failure := errors.New("directory sync failed")
	config.OperationStore = &uncertainWriteStore{OperationStore: config.OperationStore, afterQueue: func(operations.Operation) error {
		return errors.Join(operations.ErrWriteUncertain, failure)
	}}
	exec, calls := controlledExecutor()
	first, err := runner.NewRunner(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := start(t, first)
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

	restarted := newRunner(t, config, exec)
	call := receive(t, calls)
	if call.agent != agentID || saved.AgentID != "" || call.call.Work.TaskID != saved.ID || call.call.Work.OperationID != op.ID || call.call.ActionName != saved.ActionName || !reflect.DeepEqual(call.call.Input, saved.Input) {
		t.Fatalf("recovery changed the saved assignment: %+v", call)
	}
	call.reply <- output("inspect")
	settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	closeRunner(t, restarted)
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
	config := fixture()
	failure := errors.New("directory sync failed")
	var failed atomic.Bool
	config.OperationStore = &uncertainWriteStore{OperationStore: config.OperationStore, afterDispatch: func(op operations.Operation) error {
		if op.Events[len(op.Events)-1].Type == operations.TaskDispatched && failed.CompareAndSwap(false, true) {
			return errors.Join(operations.ErrWriteUncertain, failure)
		}
		return nil
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
	task := assign(t, first, op.ID, "inspect")
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

	restarted, err := runner.NewRunner(t.Context(), durableExecutor{exec}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(t, restarted) })
	recovered := receive(t, calls)
	if recovered.agent != agentID || recovered.call.Work.TaskID != task.ID || recovered.call.Work.OperationID != op.ID {
		t.Fatalf("recovery changed the reserved work: %+v", recovered)
	}
	recovered.reply <- output("inspect")
	settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	closeRunner(t, restarted)

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
	config := fixture()
	durable := config.OperationStore
	var failed atomic.Bool
	config.OperationStore = &uncertainWriteStore{OperationStore: durable, dispatch: func(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
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
	exec, calls := controlledExecutor()
	first, err := runner.NewRunner(t.Context(), exec, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := start(t, first)
	task := assign(t, first, op.ID, "inspect")
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

	restarted := newRunner(t, config, exec)
	recovered := receive(t, calls)
	if recovered.agent != agentID || task.AgentID != "" || recovered.call.Work.TaskID != task.ID || recovered.call.Work.OperationID != op.ID {
		t.Fatalf("recovery changed the queued assignment: %+v", recovered)
	}
	recovered.reply <- output("inspect")
	settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	closeRunner(t, restarted)
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
	config := fixture()
	config.MissionStore.(*missionStore).mission.Automations = map[string]missions.Automation{
		"discover": {Run: []string{"inspect"}, Continue: missions.WhenAll},
	}
	failure := errors.New("directory sync failed")
	var failed atomic.Bool
	config.OperationStore = &uncertainWriteStore{OperationStore: config.OperationStore, afterFinish: func(operations.Operation) error {
		if failed.CompareAndSwap(false, true) {
			return errors.Join(operations.ErrWriteUncertain, failure)
		}
		return nil
	}}
	exec, calls := controlledExecutor()
	var recoveries atomic.Int32
	exec.recover = func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error) {
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
	op := start(t, first)
	parent := assign(t, first, op.ID, "discover")
	receive(t, calls).reply <- output("discover",
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

	restarted := newRunner(t, config, exec)
	for range 2 {
		call := receive(t, calls)
		child, exists := savedChildren[call.call.Work.TaskID]
		if !exists || call.call.Work.OperationID != op.ID || call.agent != agentID || child.AgentID != "" || call.call.ActionName != child.ActionName || !reflect.DeepEqual(call.call.Input, child.Input) {
			t.Fatalf("recovery repeated the parent or changed an automatic child: %+v", call)
		}
		delete(savedChildren, child.ID)
		call.reply <- output("inspect")
	}
	settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	closeRunner(t, restarted)
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
	config := fixture()
	failure := errors.New("directory sync failed")
	config.OperationStore = &uncertainWriteStore{OperationStore: config.OperationStore, afterResolve: func(operations.Operation) error {
		return errors.Join(operations.ErrWriteUncertain, failure)
	}}
	exec, calls := controlledExecutor()
	var recoveries atomic.Int32
	exec.recover = func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error) {
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
	op := start(t, first)
	parent := assign(t, first, op.ID, "discover")
	receive(t, calls).reply <- output("discover")
	before := awaitOperation(t, first, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
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

	restarted := newRunner(t, config, exec)
	for range 2 {
		call := receive(t, calls)
		child, exists := savedChildren[call.call.Work.TaskID]
		if !exists || call.call.Work.OperationID != op.ID || call.agent != agentID || child.AgentID != "" || call.call.ActionName != child.ActionName || !reflect.DeepEqual(call.call.Input, child.Input) {
			t.Fatalf("recovery repeated the parent or changed a resolved child: %+v", call)
		}
		delete(savedChildren, child.ID)
		call.reply <- output("inspect")
	}
	settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Status == operations.Planning })
	if _, err := restarted.Resolve(ctx, command); err == nil || errors.Is(err, operations.ErrWriteUncertain) {
		t.Fatalf("recovery did not reject the stale decision: %v", err)
	}
	closeRunner(t, restarted)
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
	config := fixture()
	config.OperationStore = &uncertainWriteStore{OperationStore: config.OperationStore, createError: operations.ErrWriteUncertain}
	exec, calls := controlledExecutor()
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
	restarted := newRunner(t, config, exec)
	task := assign(t, restarted, opID, "inspect")
	receive(t, calls).reply <- output("inspect")
	settled := awaitOperation(t, restarted, opID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	all, err := restarted.List(t.Context())
	if err != nil || len(all) != 1 || len(settled.Tasks) != 1 || settled.Tasks[0].ID != task.ID {
		t.Fatalf("recovery duplicated work: %+v, %v", all, err)
	}
}

type uncertainWriteStore struct {
	operations.OperationStore
	createError    error
	dispatch       func(context.Context, string, operations.TaskDispatch) (operations.Operation, error)
	afterQueue     func(operations.Operation) error
	afterDispatch  func(operations.Operation) error
	afterFinish    func(operations.Operation) error
	afterResolve   func(operations.Operation) error
	afterAsk       func(operations.Operation) error
	afterAnswer    func(operations.Operation) error
	afterExpire    func(operations.Operation) error
	afterPreflight func(operations.Operation) error
	afterRecover   func(operations.Operation) error
	afterCancel    func(operations.Operation) error
	afterComplete  func(operations.Operation) error
	afterFail      func(operations.Operation) error
}

func (s *uncertainWriteStore) Create(ctx context.Context, op operations.Operation) error {
	if err := s.OperationStore.Create(ctx, op); err != nil {
		return err
	}
	return s.createError
}

func (s *uncertainWriteStore) QueueTask(ctx context.Context, operationID string, task operations.Task) (operations.Operation, error) {
	op, err := s.OperationStore.QueueTask(ctx, operationID, task)
	if err == nil && s.afterQueue != nil {
		err = s.afterQueue(op)
	}
	return op, err
}

func (s *uncertainWriteStore) DispatchTask(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
	if s.dispatch != nil {
		return s.dispatch(ctx, operationID, dispatch)
	}
	op, err := s.OperationStore.DispatchTask(ctx, operationID, dispatch)
	if err == nil && s.afterDispatch != nil {
		err = s.afterDispatch(op)
	}
	return op, err
}

func (s *uncertainWriteStore) FinishTask(ctx context.Context, operationID string, result operations.TaskResult) (operations.Operation, error) {
	op, err := s.OperationStore.FinishTask(ctx, operationID, result)
	if err == nil && s.afterFinish != nil {
		err = s.afterFinish(op)
	}
	return op, err
}

func (s *uncertainWriteStore) ResolveContinuation(ctx context.Context, operationID, taskID string, version uint64, next []actions.NextAction) (operations.Operation, error) {
	op, err := s.OperationStore.ResolveContinuation(ctx, operationID, taskID, version, next)
	if err == nil && s.afterResolve != nil {
		err = s.afterResolve(op)
	}
	return op, err
}

func (s *uncertainWriteStore) AskQuestion(ctx context.Context, operationID string, question operations.Question, version uint64) (operations.Operation, error) {
	op, err := s.OperationStore.AskQuestion(ctx, operationID, question, version)
	if err == nil && s.afterAsk != nil {
		err = s.afterAsk(op)
	}
	return op, err
}

func (s *uncertainWriteStore) AnswerQuestion(ctx context.Context, operationID, questionID, answer string) (operations.Operation, error) {
	op, err := s.OperationStore.AnswerQuestion(ctx, operationID, questionID, answer)
	if err == nil && s.afterAnswer != nil {
		err = s.afterAnswer(op)
	}
	return op, err
}

func (s *uncertainWriteStore) ExpireQuestions(ctx context.Context, operationID string, now time.Time) (operations.Operation, error) {
	op, err := s.OperationStore.ExpireQuestions(ctx, operationID, now)
	if err == nil && s.afterExpire != nil {
		err = s.afterExpire(op)
	}
	return op, err
}

func (s *uncertainWriteStore) RefreshPreflight(ctx context.Context, operationID string, records []agents.Agent) (operations.Operation, error) {
	op, err := s.OperationStore.RefreshPreflight(ctx, operationID, records)
	if err == nil && s.afterPreflight != nil {
		err = s.afterPreflight(op)
	}
	return op, err
}

func (s *uncertainWriteStore) Recover(ctx context.Context, operationID string, now time.Time) (operations.Operation, error) {
	op, err := s.OperationStore.Recover(ctx, operationID, now)
	if err == nil && s.afterRecover != nil {
		err = s.afterRecover(op)
	}
	return op, err
}

func (s *uncertainWriteStore) Cancel(ctx context.Context, operationID string) (operations.Operation, error) {
	op, err := s.OperationStore.Cancel(ctx, operationID)
	if err == nil && s.afterCancel != nil {
		err = s.afterCancel(op)
	}
	return op, err
}

func (s *uncertainWriteStore) Complete(ctx context.Context, operationID, reportID string, output json.RawMessage) (operations.Operation, error) {
	op, err := s.OperationStore.Complete(ctx, operationID, reportID, output)
	if err == nil && s.afterComplete != nil {
		err = s.afterComplete(op)
	}
	return op, err
}

func (s *uncertainWriteStore) Fail(ctx context.Context, operationID, reason string) (operations.Operation, error) {
	op, err := s.OperationStore.Fail(ctx, operationID, reason)
	if err == nil && s.afterFail != nil {
		err = s.afterFail(op)
	}
	return op, err
}

type durableExecutor struct{ executor }

func (e durableExecutor) DispatchAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return e.RunAction(ctx, agentID, call)
}
