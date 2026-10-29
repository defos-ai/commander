package agent_test

import (
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

func TestCheckpointResponsesMatchActiveExecution(t *testing.T) {
	detached := make(chan context.Context, 1)
	returned := make(chan error, 1)
	data := json.RawMessage(`{"agent_id":"worker-12345678"}`)
	executor := fixtures.CheckpointAction(t, func(ctx context.Context) error {
		detached <- context.WithoutCancel(ctx)
		// Public execution metadata cannot retarget the runtime's checkpoint
		// capability to another command or operation.
		ctx = action.WithExecution(ctx, action.Execution{CommandID: id.New(),
			Work: messages.WorkContext{OperationID: id.New(), TaskID: id.New()}})
		err := action.SaveCheckpoint(ctx, "launch-intent", data)
		returned <- err
		return err
	})
	fixture := fixtures.StartCheckpointAgent(t, executor)
	work := messages.WorkContext{OperationID: id.New(), TaskID: id.New()}
	commandID := fixture.Command(t, "checkpoint", `{}`, work)
	request := fixture.Next(t, controllerevents.CheckpointRequested)
	payload, err := controllerevents.DecodeCheckpointRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if request.AgentID != fixture.Runtime().ID() || request.Work != work || request.CorrelationID != commandID ||
		payload.CommandID != commandID || payload.Operation != controllerevents.CheckpointSave {
		t.Fatal("checkpoint request used caller-supplied execution identity")
	}
	response, err := agentevents.NewCheckpointResponse(request, agentevents.CheckpointResponse{
		CommandID: commandID, Name: "launch-intent", Data: data,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*messages.Message, *agentevents.CheckpointResponse){
		func(m *messages.Message, _ *agentevents.CheckpointResponse) { m.AgentID = "other-12345678" },
		func(m *messages.Message, _ *agentevents.CheckpointResponse) { m.CorrelationID = id.New() },
		func(m *messages.Message, _ *agentevents.CheckpointResponse) { m.Work.TaskID = id.New() },
		func(_ *messages.Message, r *agentevents.CheckpointResponse) { r.CommandID = id.New() },
		func(_ *messages.Message, r *agentevents.CheckpointResponse) { r.Name = "different-checkpoint" },
		func(_ *messages.Message, r *agentevents.CheckpointResponse) {
			r.Data = json.RawMessage(`{"agent_id":"worker-87654321"}`)
		},
	} {
		message := response
		value, err := agentevents.DecodeCheckpointResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&message, &value)
		message.Payload, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		fixture.Deliver(t, message)
	}
	// A repeated request proves the action is still waiting after all unrelated
	// responses have been processed; retries must keep the exact same identity.
	for {
		select {
		case err := <-returned:
			t.Fatalf("unrelated checkpoint response released the action: %v", err)
		case message := <-fixture.Outgoing():
			if message.Type != controllerevents.CheckpointRequested {
				continue
			}
			if !reflect.DeepEqual(request, message) {
				t.Fatal("checkpoint retry changed its identity or payload")
			}
		case <-fixture.Context().Done():
			t.Fatal("checkpoint request did not retry")
		}
		break
	}
	fixture.Deliver(t, response)
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)
	if err := <-returned; err != nil {
		t.Fatalf("matching checkpoint response did not complete the action: %v", err)
	}

	// Removing cancellation from a retained action context must not extend its
	// authority after the executor has returned.
	scope, cancel := context.WithTimeout(<-detached, time.Second)
	defer cancel()
	if _, _, err := action.LoadCheckpoint(scope, "launch-intent"); !errors.Is(err, context.Canceled) {
		t.Fatalf("completed execution retained checkpoint read access: %v", err)
	}
	if err := action.SaveCheckpoint(scope, "after-completion", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("completed execution retained checkpoint write access: %v", err)
	}
	for _, message := range fixture.History() {
		if message.Type == controllerevents.CheckpointRequested && message.ID != request.ID {
			t.Fatal("completed execution published another checkpoint request")
		}
	}
}

func TestCheckpointWaitEndsOnCancellation(t *testing.T) {
	for _, canceledBy := range []string{"caller", "host", "execution"} {
		t.Run(canceledBy, func(t *testing.T) {
			cancelRequest := make(chan context.CancelFunc, 1)
			returned := make(chan error, 1)
			finishExecution, waitDone := make(chan struct{}), make(chan struct{})
			executor := fixtures.CheckpointAction(t, func(ctx context.Context) error {
				wait := func() error {
					defer close(waitDone)
					// Owning lifetimes must still end requests made through an
					// otherwise detached context.
					ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
					defer cancel()
					cancelRequest <- cancel
					_, _, err := action.LoadCheckpoint(ctx, "launch-intent")
					returned <- err
					return err
				}
				if canceledBy != "execution" {
					return wait()
				}
				go func() { _ = wait() }()
				select {
				case <-finishExecution:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			fixture := fixtures.StartCheckpointAgent(t, executor)
			commandID := fixture.Command(t, "checkpoint", `{}`)
			request := fixture.Next(t, controllerevents.CheckpointRequested)
			t.Cleanup(func() {
				fixture.Stop(t)
				select {
				case <-waitDone:
				case <-time.After(time.Second):
					t.Error("checkpoint waiter survived agent shutdown")
				}
			})
			if payload, err := controllerevents.DecodeCheckpointRequest(request); err != nil ||
				payload.Operation != controllerevents.CheckpointLoad {
				t.Fatalf("expected a pending checkpoint read: %v", err)
			}
			switch canceledBy {
			case "host":
				fixture.Stop(t)
			case "execution":
				close(finishExecution)
				fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)
			default:
				(<-cancelRequest)()
				fixture.Outcome(t, commandID, controllerevents.CommandFailed)
				if fixture.Runtime().Status() != agent.AgentReady {
					t.Fatal("canceling checkpoint wait did not release the agent")
				}
			}
			select {
			case err := <-returned:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled checkpoint wait returned %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled checkpoint wait did not return")
			}
		})
	}
}

func TestStandaloneWorkRejectsInheritedCheckpoints(t *testing.T) {
	executor := fixtures.CheckpointAction(t, func(ctx context.Context) error {
		if _, _, err := action.LoadCheckpoint(ctx, "launch-intent"); !errors.Is(err, action.ErrCheckpointsUnavailable) {
			return errors.New("standalone work inherited checkpoint read access")
		}
		if err := action.SaveCheckpoint(ctx, "launch-intent", json.RawMessage(`{}`)); !errors.Is(err, action.ErrCheckpointsUnavailable) {
			return errors.New("standalone work inherited checkpoint write access")
		}
		return nil
	})
	runtime := fixtures.StateAgent(t, executor)
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	injected := fakes.NewUnexpectedCheckpointProvider(t)
	ctx := action.WithCheckpoints(t.Context(), injected)
	ctx = action.WithExecution(ctx, action.Execution{CommandID: id.New(),
		Work: messages.WorkContext{OperationID: id.New(), TaskID: id.New()}})
	if _, err := runtime.Work(ctx, agent.ActionCall{ActionName: "checkpoint"}); err != nil {
		t.Fatal(err)
	}
}
