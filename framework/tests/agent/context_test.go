package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestSharedContextIsReadyBeforeWorkIsAnnouncedAndBindsItsExecution(t *testing.T) {
	returned := make(chan agentevents.ContextUpdate, 1)
	retained := make(chan context.Context, 1)
	started := make(chan struct{})
	fixture := fixtures.StartContextAgent(t, true, func(ctx context.Context) error {
		close(started)
		ctx = action.WithExecution(context.WithoutCancel(ctx), action.Execution{CommandID: id.MessageID(),
			Work: messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}})
		retained <- ctx
		update, err := action.ReceiveContext(ctx)
		if err == nil {
			returned <- update
		}
		return err
	})
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}
	commandID := fixture.Command(t, fixtures.ContextActionName, `{}`, work)
	fixture.Working(t)
	update := fixtures.SharedContext(work)
	message := fixtures.SharedContextMessage(t, fixture.Runtime().ID(), commandID, work, update)
	for _, mutate := range []func(*messages.Message){
		func(m *messages.Message) { m.AgentID = "other-agent-00000001" },
		func(m *messages.Message) { m.CorrelationID = id.MessageID() },
		func(m *messages.Message) { m.Work.TaskID = id.TaskID() },
		func(m *messages.Message) { m.Work.OperationID = id.OperationID() },
		func(m *messages.Message) { m.Payload = json.RawMessage(`{"source":{}}`) },
		func(m *messages.Message) { m.Payload = bytes.Repeat([]byte(" "), agentevents.MaxContextUpdateSize+1) },
	} {
		unrelated := message
		mutate(&unrelated)
		fixture.Deliver(t, unrelated)
	}

	fixture.Deliver(t, message)
	select {
	case <-started:
		t.Fatal("action ran before its working announcement returned")
	default:
	}
	fixture.ReleaseWorking()
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)

	select {
	case got := <-returned:
		if !reflect.DeepEqual(got, update) || !bytes.Contains(got.Result, []byte("9007199254740993")) || !bytes.Contains(got.Result, []byte("1.2300e+42")) {
			t.Fatalf("shared context lost source evidence or JSON precision: %+v", got)
		}
	case <-fixture.Context().Done():
		t.Fatal("pushed context did not reach the running action")
	}
	if _, err := action.ReceiveContext(awaitContextValue(t, fixture.Context(), retained)); !errors.Is(err, context.Canceled) {
		t.Fatalf("context capability survived its action: %v", err)
	}
}

func TestSharedContextCallerCancellationLeavesItsExecutionUsable(t *testing.T) {
	cancelCall := make(chan context.CancelFunc, 1)
	canceled := make(chan error, 1)
	returned := make(chan agentevents.ContextUpdate, 1)
	fixture := fixtures.StartContextAgent(t, false, func(ctx context.Context) error {
		caller, cancel := context.WithCancel(context.WithoutCancel(ctx))
		defer cancel()
		cancelCall <- cancel
		_, err := action.ReceiveContext(caller)
		canceled <- err
		update, err := action.ReceiveContext(ctx)
		if err == nil {
			returned <- update
		}
		return err
	})
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}
	commandID := fixture.Command(t, fixtures.ContextActionName, `{}`, work)
	cancel := awaitContextValue(t, fixture.Context(), cancelCall)

	cancel()
	if err := awaitContextValue(t, fixture.Context(), canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation did not end its receive: %v", err)
	}
	update := fixtures.SharedContext(work)
	fixture.Deliver(t, fixtures.SharedContextMessage(t, fixture.Runtime().ID(), commandID, work, update))
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)

	select {
	case got := <-returned:
		if !reflect.DeepEqual(got, update) {
			t.Fatal("caller cancellation changed the execution's next update")
		}
	case <-fixture.Context().Done():
		t.Fatal("caller cancellation poisoned the execution's context inbox")
	}
}

func TestSharedContextDetachedWaitEndsWhenItsActionReturns(t *testing.T) {
	waiting := make(chan struct{})
	finished := make(chan error, 1)
	release := make(chan struct{})
	fixture := fixtures.StartContextAgent(t, false, func(ctx context.Context) error {
		go func() {
			close(waiting)
			_, err := action.ReceiveContext(context.WithoutCancel(ctx))
			finished <- err
		}()
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}
	commandID := fixture.Command(t, fixtures.ContextActionName, `{}`, work)
	awaitContextValue(t, fixture.Context(), waiting)

	close(release)
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)

	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("detached receive survived action completion: %v", err)
		}
	case <-fixture.Context().Done():
		t.Fatal("action completion did not release detached context receive")
	}
}

func TestSharedContextDetachedWaitEndsWithServingLifetimeOrTaskDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "service cancellation"
		if deadline {
			name = "task deadline"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			finished := make(chan [2]error, 1)
			fixture := fixtures.StartContextAgent(t, false, func(ctx context.Context) error {
				close(started)
				_, err := action.ReceiveContext(context.WithoutCancel(ctx))
				finished <- [2]error{err, ctx.Err()}
				return nil
			})
			work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}
			var expiry time.Time
			if deadline {
				expiry = time.Now().Add(time.Second)
			}
			command := fixtures.ContextCommand(t, fixture.Runtime().ID(), work, expiry)
			fixture.SendCommand(t, fakes.NewSecretDelivery(command))
			awaitContextValue(t, fixture.Context(), started)

			if deadline {
				fixture.Outcome(t, command.ID, controllerevents.ActionSucceeded)
			} else {
				fixture.Stop(t)
			}

			select {
			case result := <-finished:
				want := context.Canceled
				if deadline {
					want = context.DeadlineExceeded
					if result[1] != nil {
						t.Fatalf("context receive deadline canceled the running action: %v", result[1])
					}
				}
				if !errors.Is(result[0], want) {
					t.Fatalf("detached receive outlived its owner: got %v, want %v", result[0], want)
				}
			case <-time.After(time.Second):
				t.Fatal("detached context receive did not end")
			}
		})
	}
}

func TestSharedContextOverflowIsExplicitAndCannotLeakIntoAnotherExecution(t *testing.T) {
	entered := make(chan struct{}, 2)
	receive := make(chan struct{})
	finished := make(chan error, 1)
	returned := make(chan agentevents.ContextUpdate, 1)
	invocation := 0
	fixture := fixtures.StartContextAgent(t, false, func(ctx context.Context) error {
		invocation++
		entered <- struct{}{}
		if invocation == 1 {
			select {
			case <-receive:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := action.ReceiveContext(ctx)
			finished <- err
			return nil
		}
		update, err := action.ReceiveContext(ctx)
		if err == nil {
			returned <- update
		}
		return err
	})
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}
	commandID := fixture.Command(t, fixtures.ContextActionName, `{}`, work)
	awaitContextValue(t, fixture.Context(), entered)
	update := fixtures.SharedContext(work)
	message := fixtures.SharedContextMessage(t, fixture.Runtime().ID(), commandID, work, update)

	for range 33 {
		fixture.Deliver(t, message)
	}
	close(receive)
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)
	if err := awaitContextValue(t, fixture.Context(), finished); !errors.Is(err, action.ErrContextOverflow) {
		t.Fatalf("context overflow was silent or stopped unrelated work: %v", err)
	}
	fixture.Deliver(t, message)
	work.TaskID = id.TaskID()
	secondID := fixture.Command(t, fixtures.ContextActionName, `{}`, work)
	awaitContextValue(t, fixture.Context(), entered)
	fixture.Deliver(t, message)
	fresh := fixtures.SharedContext(work)
	fixture.Deliver(t, fixtures.SharedContextMessage(t, fixture.Runtime().ID(), secondID, work, fresh))
	fixture.Outcome(t, secondID, controllerevents.ActionSucceeded)

	select {
	case got := <-returned:
		if !reflect.DeepEqual(got, fresh) {
			t.Fatal("a later execution received stale context or inherited overflow")
		}
	case <-fixture.Context().Done():
		t.Fatal("context overflow disabled a later execution")
	}
}

func TestStandaloneWorkClearsInheritedSharedContext(t *testing.T) {
	executor := fixtures.CheckpointAction(t, func(ctx context.Context) error {
		_, err := action.ReceiveContext(ctx)
		if !errors.Is(err, action.ErrContextUnavailable) {
			t.Errorf("standalone work retained context reception: %v", err)
		}
		return nil
	})
	runtime := fixtures.StateAgent(t, executor)
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	ctx := action.WithContextUpdates(t.Context(), fakes.ContextProvider(func(context.Context) (agentevents.ContextUpdate, error) {
		t.Error("standalone work called its caller's shared context provider")
		return agentevents.ContextUpdate{}, nil
	}))

	_, err := runtime.Work(ctx, agent.ActionCall{ActionName: "checkpoint"})

	if err != nil {
		t.Fatal(err)
	}
}

func awaitContextValue[T any](t *testing.T, ctx context.Context, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-ctx.Done():
		t.Fatal("agent did not reach the expected context receive state")
		var zero T
		return zero
	}
}
