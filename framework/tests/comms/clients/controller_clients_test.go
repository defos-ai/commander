package clients_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"commander/framework/comms"
	adapterevents "commander/framework/comms/clients/adapter/events"
	controllercommands "commander/framework/comms/clients/controller/commands"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	"commander/framework/domain"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/id"
	fscomms "commander/providers/fs/comms"
)

func TestControllerEventClientRoutesAgentLifecycle(t *testing.T) {
	for _, kind := range []messages.MessageType{
		controllerevents.AgentStarted,
		controllerevents.AgentHeartbeat,
		controllerevents.AgentUpdated,
		controllerevents.AgentStopped,
	} {
		t.Run(string(kind), func(t *testing.T) {
			// Arrange
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			bus := newClientBus(t)
			sub := subscribeClientBus(t, bus, messages.ControllerEvents(messages.DefaultControllerType))
			client := controllerevents.Client{Bus: bus}
			description := agentmodel.AgentSnapshot{
				ID: "web-quality-agent-a1b2c3d4", Type: "web-quality", Status: agentmodel.AgentReady,
				Config: []byte(`{"network_access":true}`),
			}
			if kind == controllerevents.AgentStopped {
				description.Status = agentmodel.AgentStopped
			}
			correlationID := id.MessageID()

			// Act
			if err := client.State(ctx, kind, description, correlationID); err != nil {
				t.Fatal(err)
			}
			received, err := sub.Next(ctx)
			var snapshot agentmodel.AgentSnapshot
			if err == nil {
				err = domain.Decode(received.Payload, &snapshot)
			}

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if received.Type != kind || received.AgentID != description.ID || received.CorrelationID != correlationID || !id.Valid(received.ID) {
				t.Fatalf("unexpected lifecycle event: %+v", received)
			}
			if !reflect.DeepEqual(snapshot, description) {
				t.Fatalf("snapshot changed in transit: got %+v, want %+v", snapshot, description)
			}
		})
	}
}

func TestControllerCommandClientRoutesRequestAndCorrelatedReply(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	bus := newClientBus(t)
	requests := subscribeClientBus(t, bus, messages.ControllerRequests(messages.DefaultControllerType))
	const adapterID = "mcp-adapter-a1b2c3d4"
	const agentID = "web-quality-agent-a1b2c3d4"
	client, err := controllercommands.NewClient(ctx, bus, adapterID, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	call := actionmodel.ActionCall{ActionName: "inspect"}
	want := actionmodel.ActionOutput{ActionName: "inspect", Output: []byte(`{"status":"ready"}`)}
	type outcome struct {
		output actionmodel.ActionOutput
		err    error
	}
	completed := make(chan outcome, 1)

	// Act
	go func() {
		output, err := client.RunAction(ctx, agentID, call)
		completed <- outcome{output: output, err: err}
	}()
	request, err := requests.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var decoded controllercommands.CommandEnvelope
	if err := domain.Decode(request.Payload, &decoded); err != nil {
		t.Fatal(err)
	}
	var params controllercommands.RunActionCommand
	if err := domain.Decode(decoded.Parameters, &params); err != nil {
		t.Fatal(err)
	}
	reply, err := adapterevents.NewReply(request.AdapterID, request.ID, want, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := (adapterevents.Client{Bus: bus}).Publish(ctx, reply); err != nil {
		t.Fatal(err)
	}
	result := <-completed

	// Assert
	if request.Type != controllercommands.AdapterRequest || request.AdapterID != adapterID || !id.Valid(request.ID) {
		t.Fatalf("unexpected controller request: %+v", request)
	}
	deadline, _ := ctx.Deadline()
	if decoded.Method != controllercommands.MethodRunAction || !decoded.Deadline.Equal(deadline) || params.AgentID != agentID || !reflect.DeepEqual(params.Call, call) {
		t.Fatalf("command changed in transit: request=%+v parameters=%+v", decoded, params)
	}
	if result.err != nil || !reflect.DeepEqual(result.output, want) {
		t.Fatalf("unexpected command result: output=%+v error=%v", result.output, result.err)
	}
}

func TestControllerCommandClientRoutesCancellation(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	bus := newClientBus(t)
	requests := subscribeClientBus(t, bus, messages.ControllerRequests(messages.DefaultControllerType))
	cancellations := subscribeClientBus(t, bus, messages.ControllerCancellations(messages.DefaultControllerType))
	const adapterID = "mcp-adapter-a1b2c3d4"
	client, err := controllercommands.NewClient(ctx, bus, adapterID, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	callCtx, cancelCall := context.WithCancel(ctx)
	defer cancelCall()
	completed := make(chan error, 1)

	// Act
	go func() {
		_, err := client.StopAgent(callCtx, "web-quality-agent-a1b2c3d4")
		completed <- err
	}()
	request, err := requests.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancelCall()
	callErr := <-completed
	cancellation, err := cancellations.Next(ctx)

	// Assert
	if !errors.Is(callErr, context.Canceled) {
		t.Fatalf("expected canceled call, got %v", callErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if cancellation.Type != controllercommands.AdapterRequestCanceled || cancellation.AdapterID != adapterID || cancellation.CorrelationID != request.ID {
		t.Fatalf("cancellation did not identify the original request: %+v", cancellation)
	}
	if !id.Valid(cancellation.ID) || cancellation.ID == request.ID {
		t.Fatalf("cancellation needs an independent message ID: request=%s cancellation=%s", request.ID, cancellation.ID)
	}
}

func newClientBus(t *testing.T) comms.Bus {
	t.Helper()
	bus, err := fscomms.NewFsBus(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

func subscribeClientBus(t *testing.T, bus comms.Bus, topic string) comms.Subscription {
	t.Helper()
	sub, err := bus.Subscribe(t.Context(), topic)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	return sub
}
