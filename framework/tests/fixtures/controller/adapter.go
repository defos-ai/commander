package fixtures

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	adapter "github.com/defos-ai/commander/framework/controller/consumers/adapter"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
)

const AdapterID = "fixture-adapter-00000001"
const ControllerID = "default-controller-00000001"

type AdapterRun struct {
	done chan struct{}
	err  error
}

func RunAdapter(t *testing.T, ctx context.Context, consumer *adapter.Consumer) *AdapterRun {
	t.Helper()
	lifetime, cancel := context.WithCancel(ctx)
	run := &AdapterRun{done: make(chan struct{})}
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

func Adapter(t *testing.T) (context.Context, *fakes.AdapterBus, *fakes.AdapterBackend, *adapter.Consumer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	bus := fakes.NewAdapterBus(AdapterID, ControllerID)
	backend := fakes.NewAdapterBackend()
	consumer, err := adapter.NewConsumer(ctx, bus, "default", ControllerID, backend, fakes.UnusedOperations{}, QuietLogger())
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

func QuietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func Publish(t *testing.T, ctx context.Context, bus comms.Bus, topic string, m messages.Message) {
	t.Helper()
	if err := bus.Publish(ctx, topic, m); err != nil {
		t.Fatal(err)
	}
}
func AdapterRequest(t *testing.T) messages.Message {
	t.Helper()
	parameters, err := json.Marshal(commands.DiscoverActionsCommand{AgentID: AgentID})
	if err != nil {
		t.Fatal(err)
	}
	request, err := messages.NewAdapterMessage(messages.AdapterRequest, AdapterID,
		commands.CommandEnvelope{Method: commands.MethodDiscoverActions, Parameters: parameters, Deadline: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func (r *AdapterRun) Done() <-chan struct{} { return r.done }
func (r *AdapterRun) Err() error            { <-r.done; return r.err }

func DrainAdapterReplyTopic(t *testing.T, ctx context.Context, bus *fakes.AdapterBus) {
	t.Helper()
	draining, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-bus.Topic(messages.AdapterReplies(AdapterID)):
			case <-draining.Done():
				return
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func AdapterRequestWithDeadline(t *testing.T, deadline time.Time) messages.Message {
	t.Helper()
	request := AdapterRequest(t)
	var envelope commands.CommandEnvelope
	if err := json.Unmarshal(request.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Deadline = deadline
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	request.Payload = payload
	return request
}
