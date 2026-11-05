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
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestSharedDataIsReadyBeforeWorkIsAnnouncedAndBindsItsExecution(t *testing.T) {
	returned := make(chan agentevents.SharedData, 1)
	retained := make(chan context.Context, 1)
	started := make(chan struct{})
	fixture := fixtures.StartSharingAgent(t, true, func(ctx context.Context) error {
		close(started)
		ctx = action.WithExecution(context.WithoutCancel(ctx), action.Execution{CommandID: id.MessageID(),
			OperationID: id.OperationID(), TaskID: id.TaskID()})
		retained <- ctx
		update, err := action.ReceiveSharedData(ctx)
		if err == nil {
			returned <- update
		}
		return err
	})
	operationID, taskID := id.OperationID(), id.TaskID()
	commandID := fixture.Command(t, fixtures.SharingActionName, `{}`, operationID, taskID)
	fixture.Working(t)
	update := fixtures.SharedData(operationID, taskID)
	message := fixtures.SharedDataMessage(t, fixture.Runtime().ID(), commandID, operationID, taskID, update)
	for _, mutate := range []func(*messages.Message){
		func(m *messages.Message) { m.AgentID = "other-agent-00000001" },
		func(m *messages.Message) { m.CorrelationID = id.MessageID() },
		func(m *messages.Message) { m.TaskID = id.TaskID() },
		func(m *messages.Message) { m.OperationID = id.OperationID() },
		func(m *messages.Message) { m.OperationID = "" },
		func(m *messages.Message) { m.TaskID = "" },
		func(m *messages.Message) { m.Payload = json.RawMessage(`{"source":{}}`) },
		func(m *messages.Message) { m.Payload = bytes.Repeat([]byte(" "), agentevents.MaxSharedDataSize+1) },
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
			t.Fatalf("shared data lost source evidence or JSON precision: %+v", got)
		}
	case <-fixture.Context().Done():
		t.Fatal("shared data did not reach the running action")
	}
	if _, err := action.ReceiveSharedData(awaitContextValue(t, fixture.Context(), retained)); !errors.Is(err, context.Canceled) {
		t.Fatalf("shared data capability survived its action: %v", err)
	}
}

func TestSharedDataCallerCancellationLeavesItsExecutionUsable(t *testing.T) {
	cancelCall := make(chan context.CancelFunc, 1)
	canceled := make(chan error, 1)
	returned := make(chan agentevents.SharedData, 1)
	fixture := fixtures.StartSharingAgent(t, false, func(ctx context.Context) error {
		caller, cancel := context.WithCancel(context.WithoutCancel(ctx))
		defer cancel()
		cancelCall <- cancel
		_, err := action.ReceiveSharedData(caller)
		canceled <- err
		update, err := action.ReceiveSharedData(ctx)
		if err == nil {
			returned <- update
		}
		return err
	})
	operationID, taskID := id.OperationID(), id.TaskID()
	commandID := fixture.Command(t, fixtures.SharingActionName, `{}`, operationID, taskID)
	cancel := awaitContextValue(t, fixture.Context(), cancelCall)

	cancel()
	if err := awaitContextValue(t, fixture.Context(), canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation did not end its receive: %v", err)
	}
	update := fixtures.SharedData(operationID, taskID)
	fixture.Deliver(t, fixtures.SharedDataMessage(t, fixture.Runtime().ID(), commandID, operationID, taskID, update))
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)

	select {
	case got := <-returned:
		if !reflect.DeepEqual(got, update) {
			t.Fatal("caller cancellation changed the execution's next update")
		}
	case <-fixture.Context().Done():
		t.Fatal("caller cancellation poisoned the execution's sharing inbox")
	}
}

func TestSharedDataBatchBoundariesReachTheirExecutionWithoutAcceptingInvalidUpdates(t *testing.T) {
	received := make(chan []agentevents.SharedData, 1)
	fixture := fixtures.StartSharingAgent(t, true, func(ctx context.Context) error {
		var updates []agentevents.SharedData
		for range 3 {
			update, err := action.ReceiveSharedData(ctx)
			if err != nil {
				return err
			}
			updates = append(updates, update)
		}
		received <- updates
		return nil
	})
	operationID, taskID := id.OperationID(), id.TaskID()
	commandID := fixture.Command(t, fixtures.SharingActionName, `{}`, operationID, taskID)
	fixture.Working(t)
	expected := fixtures.SharedBatchData(operationID, taskID)
	for _, invalid := range []struct {
		index  int
		mutate func(*agentevents.SharedData)
	}{
		{index: 0, mutate: func(update *agentevents.SharedData) { update.Source.AgentID = "scanner-agent-00000001" }},
		{index: 0, mutate: func(update *agentevents.SharedData) { update.Result = json.RawMessage(`{}`) }},
		{index: 0, mutate: func(update *agentevents.SharedData) { update.Batch.TaskIDs[1] = update.Batch.TaskIDs[0] }},
		{index: 0, mutate: func(update *agentevents.SharedData) { update.Batch.TaskIDs[0] = taskID }},
		{index: 0, mutate: func(update *agentevents.SharedData) { update.Source.OperationID = id.OperationID() }},
		{index: 1, mutate: func(update *agentevents.SharedData) { update.Batch = expected[0].Clone().Batch }},
		{index: 2, mutate: func(update *agentevents.SharedData) {
			update.Batch.Outcomes[update.Batch.TaskIDs[0]] = operations.TaskInterrupted
		}},
	} {
		message := fixtures.SharedDataMessage(t, fixture.Runtime().ID(), commandID, operationID, taskID, expected[invalid.index])
		update := expected[invalid.index].Clone()
		invalid.mutate(&update)
		payload, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		message.Payload = payload
		fixture.Deliver(t, message)
	}

	for _, update := range expected {
		fixture.Deliver(t, fixtures.SharedDataMessage(t, fixture.Runtime().ID(), commandID, operationID, taskID, update))
	}
	fixture.ReleaseWorking()
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)

	got := awaitContextValue(t, fixture.Context(), received)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("batch stream was corrupted or accepted an invalid update: got=%+v want=%+v", got, expected)
	}
	for _, update := range []agentevents.SharedData{got[0], got[2]} {
		ctx := action.WithSharedData(t.Context(), fakes.SharedDataProvider(func(context.Context) (agentevents.SharedData, error) {
			return update, nil
		}))
		owned, err := action.ReceiveSharedData(ctx)
		if err != nil {
			t.Fatal(err)
		}
		member := owned.Batch.TaskIDs[0]
		owned.Batch.TaskIDs[0] = id.TaskID()
		if owned.Batch.Outcomes != nil {
			owned.Batch.Outcomes[member] = operations.TaskInterrupted
		}
		if update.Batch.TaskIDs[0] != member || update.Batch.Outcomes != nil && update.Batch.Outcomes[member] != operations.TaskSucceeded {
			t.Fatal("receiving an update exposed the provider's batch membership or outcomes")
		}
	}
}

func TestSharedDataDetachedWaitEndsWhenItsActionReturns(t *testing.T) {
	waiting := make(chan struct{})
	finished := make(chan error, 1)
	release := make(chan struct{})
	fixture := fixtures.StartSharingAgent(t, false, func(ctx context.Context) error {
		go func() {
			close(waiting)
			_, err := action.ReceiveSharedData(context.WithoutCancel(ctx))
			finished <- err
		}()
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	operationID, taskID := id.OperationID(), id.TaskID()
	commandID := fixture.Command(t, fixtures.SharingActionName, `{}`, operationID, taskID)
	awaitContextValue(t, fixture.Context(), waiting)

	close(release)
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)

	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("detached receive survived action completion: %v", err)
		}
	case <-fixture.Context().Done():
		t.Fatal("action completion did not release detached shared data receive")
	}
}

func TestSharedDataDetachedWaitEndsWithServingLifetimeOrTaskDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "service cancellation"
		if deadline {
			name = "task deadline"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			finished := make(chan [2]error, 1)
			fixture := fixtures.StartSharingAgent(t, false, func(ctx context.Context) error {
				close(started)
				_, err := action.ReceiveSharedData(context.WithoutCancel(ctx))
				finished <- [2]error{err, ctx.Err()}
				return nil
			})
			operationID, taskID := id.OperationID(), id.TaskID()
			var expiry time.Time
			if deadline {
				expiry = time.Now().Add(time.Second)
			}
			command := fixtures.SharingCommand(t, fixture.Runtime().ID(), operationID, taskID, expiry)
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
						t.Fatalf("shared data receive deadline canceled the running action: %v", result[1])
					}
				}
				if !errors.Is(result[0], want) {
					t.Fatalf("detached receive outlived its owner: got %v, want %v", result[0], want)
				}
			case <-time.After(time.Second):
				t.Fatal("detached shared data receive did not end")
			}
		})
	}
}

func TestSharedDataOverflowIsExplicitAndCannotLeakIntoAnotherExecution(t *testing.T) {
	entered := make(chan struct{}, 2)
	receive := make(chan struct{})
	finished := make(chan error, 1)
	returned := make(chan agentevents.SharedData, 1)
	invocation := 0
	fixture := fixtures.StartSharingAgent(t, false, func(ctx context.Context) error {
		invocation++
		entered <- struct{}{}
		if invocation == 1 {
			select {
			case <-receive:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := action.ReceiveSharedData(ctx)
			finished <- err
			return nil
		}
		update, err := action.ReceiveSharedData(ctx)
		if err == nil {
			returned <- update
		}
		return err
	})
	operationID, taskID := id.OperationID(), id.TaskID()
	commandID := fixture.Command(t, fixtures.SharingActionName, `{}`, operationID, taskID)
	awaitContextValue(t, fixture.Context(), entered)
	update := fixtures.SharedData(operationID, taskID)
	message := fixtures.SharedDataMessage(t, fixture.Runtime().ID(), commandID, operationID, taskID, update)

	for range 33 {
		fixture.Deliver(t, message)
	}
	close(receive)
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)
	if err := awaitContextValue(t, fixture.Context(), finished); !errors.Is(err, action.ErrSharedDataOverflow) {
		t.Fatalf("sharing overflow was silent or stopped unrelated work: %v", err)
	}
	fixture.Deliver(t, message)
	taskID = id.TaskID()
	secondID := fixture.Command(t, fixtures.SharingActionName, `{}`, operationID, taskID)
	awaitContextValue(t, fixture.Context(), entered)
	fixture.Deliver(t, message)
	fresh := fixtures.SharedData(operationID, taskID)
	fixture.Deliver(t, fixtures.SharedDataMessage(t, fixture.Runtime().ID(), secondID, operationID, taskID, fresh))
	fixture.Outcome(t, secondID, controllerevents.ActionSucceeded)

	select {
	case got := <-returned:
		if !reflect.DeepEqual(got, fresh) {
			t.Fatal("a later execution received stale shared data or inherited overflow")
		}
	case <-fixture.Context().Done():
		t.Fatal("sharing overflow disabled a later execution")
	}
}

func TestStandaloneWorkClearsInheritedSharedData(t *testing.T) {
	executor := fixtures.CheckpointAction(t, func(ctx context.Context) error {
		_, err := action.ReceiveSharedData(ctx)
		if !errors.Is(err, action.ErrSharedDataUnavailable) {
			t.Errorf("standalone work retained shared data reception: %v", err)
		}
		return nil
	})
	runtime := fixtures.StateAgent(t, executor)
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	ctx := action.WithSharedData(t.Context(), fakes.SharedDataProvider(func(context.Context) (agentevents.SharedData, error) {
		t.Error("standalone work called its caller's shared data provider")
		return agentevents.SharedData{}, nil
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
		t.Fatal("agent did not reach the expected shared data receive state")
		var zero T
		return zero
	}
}
