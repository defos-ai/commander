package runner_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"commander/framework/comms/clients/controller/commands"
	"commander/framework/controller/runner"
	"commander/framework/domain/actions"
	"commander/framework/domain/executions"
	"commander/framework/domain/operations"
	"commander/framework/id"
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
	_, assignErr := first.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, AgentID: agentID, ActionName: "inspect"})
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
	_, assignErr := first.Assign(t.Context(), commands.AssignTaskCommand{OperationID: opID, AgentID: agentID, ActionName: "inspect"})
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
	createError   error
	afterDispatch func(operations.Operation) error
}

func (s *uncertainWriteStore) Create(ctx context.Context, op operations.Operation) error {
	if err := s.OperationStore.Create(ctx, op); err != nil {
		return err
	}
	return s.createError
}

func (s *uncertainWriteStore) DispatchTask(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
	op, err := s.OperationStore.DispatchTask(ctx, operationID, dispatch)
	if err == nil && s.afterDispatch != nil {
		err = s.afterDispatch(op)
	}
	return op, err
}

type durableExecutor struct{ executor }

func (e durableExecutor) DispatchAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return e.RunAction(ctx, agentID, call)
}
