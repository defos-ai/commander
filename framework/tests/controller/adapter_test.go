package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	adapterevents "github.com/defos-ai/commander/framework/comms/clients/adapter/events"
	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	adapter "github.com/defos-ai/commander/framework/controller/consumers/adapter"
	"github.com/defos-ai/commander/framework/domain/agents"
)

const adapterID = "fixture-adapter-00000001"
const controllerID = "default-controller-00000001"

func TestControllerClientCorrelatesRepliesAndCancelsIndependently(t *testing.T) {
	ctx, bus, backend, consumer := adapterFixture(t)
	runAdapter(t, ctx, consumer)
	client, err := commands.NewClient(ctx, bus, adapterID, quietLogger())
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
	first := call(firstCtx, agentID)
	firstWork := receive(t, ctx, backend.started)
	second := call(ctx, "fixture-agent-00000002")
	secondWork := receive(t, ctx, backend.started)

	secondWork.finish <- agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: secondWork.agentID}}
	if result := receive(t, ctx, second); result.err != nil || result.record.ID != secondWork.agentID {
		t.Fatalf("reply reached wrong caller: %+v", result)
	}
	select {
	case result := <-first:
		t.Fatalf("first call completed prematurely: %+v", result)
	default:
	}
	cancel()
	if result := receive(t, ctx, first); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("call cancellation: %+v", result)
	}
	receive(t, ctx, firstWork.ctx.Done())
	third := call(ctx, agentID)
	thirdWork := receive(t, ctx, backend.started)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if result := receive(t, ctx, third); !errors.Is(result.err, comms.ErrClosed) {
		t.Fatalf("Close did not release pending call with ErrClosed: %v", result.err)
	}
	receive(t, ctx, thirdWork.ctx.Done())
}

func TestAdapterReplaysOutcomeAfterFailedPublication(t *testing.T) {
	ctx, bus, backend, consumer := adapterFixture(t)
	running := runAdapter(t, ctx, consumer)
	request := adapterRequest(t)
	publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	work := receive(t, ctx, backend.started)
	publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	bus.failReply.Store(true)

	work.finish <- agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: work.agentID}}
	original := receive(t, ctx, bus.replies)
	receive(t, ctx, running.done)
	if !errors.Is(running.err, errReplyPublication) {
		t.Fatalf("publication failure was lost: %v", running.err)
	}
	if backend.calls.Load() != 1 {
		t.Fatal("duplicate delivery executed twice")
	}
	for len(bus.replies) > 0 {
		<-bus.replies
	}
	runAdapter(t, ctx, consumer)
	publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	replay := receive(t, ctx, bus.replies)

	if !reflect.DeepEqual(original, replay) || backend.calls.Load() != 1 {
		t.Fatalf("replay changed the outcome or repeated work: original=%+v replay=%+v calls=%d", original, replay, backend.calls.Load())
	}
}

func TestAdapterRemembersCancellationBeforeRequest(t *testing.T) {
	ctx, bus, backend, consumer := adapterFixture(t)
	runAdapter(t, ctx, consumer)
	request := adapterRequest(t)
	cancellation, err := messages.NewAdapterMessage(messages.AdapterRequestCanceled, adapterID, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancellation.CorrelationID = request.ID
	publish(t, ctx, bus, messages.ControllerCancellations("default"), cancellation)
	if ack := receive(t, ctx, bus.acked); ack != cancellation.ID {
		t.Fatalf("unexpected acknowledgement: %s", ack)
	}

	publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	response := receive(t, ctx, bus.replies)
	var reply adapterevents.ControllerReply
	if err := json.Unmarshal(response.Payload, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error == nil || !errors.Is(adapterevents.DecodeError(reply.Error), context.Canceled) || backend.calls.Load() != 0 {
		t.Fatalf("pre-cancelled request executed: reply=%+v calls=%d", reply, backend.calls.Load())
	}
}

type clientResult struct {
	record agents.Agent
	err    error
}
type adapterWork struct {
	ctx     context.Context
	agentID string
	finish  chan agents.Agent
}
type adapterBackend struct {
	commands.Controller
	started       chan adapterWork
	calls         atomic.Int64
	waitForFinish bool
}

func (b *adapterBackend) DiscoverActions(ctx context.Context, agentID string, _ *agents.DiscoveryScope) (agents.Agent, error) {
	b.calls.Add(1)
	work := adapterWork{ctx: ctx, agentID: agentID, finish: make(chan agents.Agent, 1)}
	select {
	case b.started <- work:
	case <-ctx.Done():
		return agents.Agent{}, ctx.Err()
	}
	if b.waitForFinish {
		return <-work.finish, nil
	}
	select {
	case result := <-work.finish:
		return result, nil
	case <-ctx.Done():
		return agents.Agent{}, ctx.Err()
	}
}

type unusedOperations struct{ commands.Operations }
type adapterRun struct {
	done chan struct{}
	err  error
}

func runAdapter(t *testing.T, ctx context.Context, consumer *adapter.Consumer) *adapterRun {
	t.Helper()
	lifetime, cancel := context.WithCancel(ctx)
	run := &adapterRun{done: make(chan struct{})}
	go func() { defer close(run.done); run.err = consumer.Consume(lifetime) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-run.done:
		case <-time.After(time.Second):
			t.Error("adapter workers did not stop")
		}
	})
	return run
}

func adapterFixture(t *testing.T) (context.Context, *adapterBus, *adapterBackend, *adapter.Consumer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	bus := &adapterBus{topics: map[string]chan messages.Message{
		"controllers.default.commands":              make(chan messages.Message, 32),
		messages.ControllerCancellations("default"): make(chan messages.Message, 32),
		messages.AdapterReplies(adapterID):          make(chan messages.Message, 32),
	}, replies: make(chan messages.Message, 32), acked: make(chan string, 64), rejected: make(chan string, 64)}
	backend := &adapterBackend{started: make(chan adapterWork, 16)}
	consumer, err := adapter.NewConsumer(ctx, bus, "default", controllerID, backend, unusedOperations{}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := consumer.Close(); err != nil {
			t.Error(err)
		}
	})
	return ctx, bus, backend, consumer
}

var errReplyPublication = errors.New("reply publication failed")

type adapterBus struct {
	topics    map[string]chan messages.Message
	replies   chan messages.Message
	acked     chan string
	rejected  chan string
	failReply atomic.Bool
}

func (b *adapterBus) Publish(ctx context.Context, topic string, m messages.Message) error {
	if m.Type == adapterevents.ControllerResponse {
		select {
		case b.replies <- m:
		case <-ctx.Done():
			return ctx.Err()
		}
		if b.failReply.Swap(false) {
			return errReplyPublication
		}
	}
	select {
	case b.topics[topic] <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *adapterBus) Subscribe(_ context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	ownerID := controllerID
	if inbox.Topic == messages.AdapterReplies(adapterID) {
		ownerID = adapterID
	}
	topic, exists := b.topics[inbox.Topic]
	if !exists || inbox.Name != ownerID || inbox.Mode != comms.TransientExclusive {
		return nil, fmt.Errorf("unexpected inbox configuration: %+v", inbox)
	}
	return adapterSubscription{messages: topic, acked: b.acked, rejected: b.rejected}, nil
}
func (*adapterBus) Close() error { return nil }

type adapterSubscription struct {
	messages <-chan messages.Message
	acked    chan<- string
	rejected chan<- string
}

func (s adapterSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case m := <-s.messages:
		return adapterDelivery{testDelivery: testDelivery{message: m, onAck: func(ctx context.Context) error {
			select {
			case s.acked <- m.ID:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, rejected: s.rejected}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type adapterDelivery struct {
	testDelivery
	rejected chan<- string
}

func (d adapterDelivery) Reject(ctx context.Context) error {
	select {
	case d.rejected <- d.message.ID:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (adapterSubscription) Close() error { return nil }
func quietLogger() *slog.Logger          { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func publish(t *testing.T, ctx context.Context, bus comms.Bus, topic string, m messages.Message) {
	t.Helper()
	if err := bus.Publish(ctx, topic, m); err != nil {
		t.Fatal(err)
	}
}
func adapterRequest(t *testing.T) messages.Message {
	t.Helper()
	parameters, err := json.Marshal(commands.DiscoverActionsCommand{AgentID: agentID})
	if err != nil {
		t.Fatal(err)
	}
	request, err := messages.NewAdapterMessage(messages.AdapterRequest, adapterID,
		commands.CommandEnvelope{Method: commands.MethodDiscoverActions, Parameters: parameters, Deadline: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	return request
}
