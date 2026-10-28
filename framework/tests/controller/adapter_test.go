package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/comms"
	adapterevents "github.com/defos-ai/commander/framework/comms/clients/adapter/events"
	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/agents"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestControllerClientCorrelatesRepliesAndCancelsIndependently(t *testing.T) {
	ctx, bus, backend, consumer := fixtures.Adapter(t)
	fixtures.RunAdapter(t, ctx, consumer)
	client, err := commands.NewClient(ctx, bus, fixtures.AdapterID, fixtures.QuietLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	call := func(ctx context.Context, agentID string) <-chan clientResult {
		finished := make(chan clientResult, 1)
		go func() {
			record, err := client.DiscoverActions(ctx, agentID, nil)
			finished <- clientResult{record, err}
		}()
		return finished
	}
	firstCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	first := call(firstCtx, fixtures.AgentID)
	firstWork := fixtures.Receive(t, ctx, backend.Started())
	second := call(ctx, "fixture-agent-00000002")
	secondWork := fixtures.Receive(t, ctx, backend.Started())

	secondWork.Finish <- agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: secondWork.AgentID}}
	if result := fixtures.Receive(t, ctx, second); result.err != nil || result.record.ID != secondWork.AgentID {
		t.Fatalf("reply reached wrong caller: %+v", result)
	}
	select {
	case result := <-first:
		t.Fatalf("first call completed prematurely: %+v", result)
	default:
	}
	cancel()
	if result := fixtures.Receive(t, ctx, first); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("call cancellation: %+v", result)
	}
	fixtures.Receive(t, ctx, firstWork.Context.Done())
	third := call(ctx, fixtures.AgentID)
	thirdWork := fixtures.Receive(t, ctx, backend.Started())
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if result := fixtures.Receive(t, ctx, third); !errors.Is(result.err, comms.ErrClosed) {
		t.Fatalf("Close did not release pending call with ErrClosed: %v", result.err)
	}
	fixtures.Receive(t, ctx, thirdWork.Context.Done())
}

func TestAdapterReplaysOutcomeAfterFailedPublication(t *testing.T) {
	ctx, bus, backend, consumer := fixtures.Adapter(t)
	running := fixtures.RunAdapter(t, ctx, consumer)
	request := fixtures.AdapterRequest(t)
	fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	work := fixtures.Receive(t, ctx, backend.Started())
	fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	bus.FailNextReply()

	work.Finish <- agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: work.AgentID}}
	original := fixtures.Receive(t, ctx, bus.Replies())
	fixtures.Receive(t, ctx, running.Done())
	if !errors.Is(running.Err(), fakes.ErrReplyPublication) {
		t.Fatalf("publication failure was lost: %v", running.Err())
	}
	if backend.Calls() != 1 {
		t.Fatal("duplicate delivery executed twice")
	}
	for len(bus.Replies()) > 0 {
		<-bus.Replies()
	}
	fixtures.RunAdapter(t, ctx, consumer)
	fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	replay := fixtures.Receive(t, ctx, bus.Replies())

	if !reflect.DeepEqual(original, replay) || backend.Calls() != 1 {
		t.Fatalf("replay changed the outcome or repeated work: original=%+v replay=%+v calls=%d", original, replay, backend.Calls())
	}
}

func TestAdapterRemembersCancellationBeforeRequest(t *testing.T) {
	ctx, bus, backend, consumer := fixtures.Adapter(t)
	fixtures.RunAdapter(t, ctx, consumer)
	request := fixtures.AdapterRequest(t)
	cancellation, err := messages.NewAdapterMessage(messages.AdapterRequestCanceled, fixtures.AdapterID, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancellation.CorrelationID = request.ID
	fixtures.Publish(t, ctx, bus, messages.ControllerCancellations("default"), cancellation)
	if ack := fixtures.Receive(t, ctx, bus.Acknowledged()); ack != cancellation.ID {
		t.Fatalf("unexpected acknowledgement: %s", ack)
	}

	fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	response := fixtures.Receive(t, ctx, bus.Replies())
	var reply adapterevents.ControllerReply
	if err := json.Unmarshal(response.Payload, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error == nil || !errors.Is(adapterevents.DecodeError(reply.Error), context.Canceled) || backend.Calls() != 0 {
		t.Fatalf("pre-cancelled request executed: reply=%+v calls=%d", reply, backend.Calls())
	}
}

type clientResult struct {
	record agents.Agent
	err    error
}
