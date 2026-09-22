package agent_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"commander/framework/agent/consumers"
	"commander/framework/agent/environment"
	"commander/framework/comms"
	agentcommands "commander/framework/comms/clients/agent/commands"
	agentevents "commander/framework/comms/clients/agent/events"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	"commander/framework/domain/actions"
	"commander/framework/id"
)

func TestDuplicateCommandAndMissingReceiptDoNotRepeatExecution(t *testing.T) {
	// Arrange: the bus delivers results, but the controller's receipt is lost.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	bus := &resultBus{results: make(chan messages.Message, 16)}
	runtime := &countingRuntime{}
	outcomes := &consumers.Outcomes{}
	consumer := consumers.ActionConsumer{Runtime: runtime, AgentID: "fixture-agent-12345678", Outcomes: outcomes,
		Events: controllerevents.Client{Bus: bus}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	command, err := agentcommands.NewRunAction(consumer.AgentID, actions.ActionCall{ActionName: "inspect", Input: []byte(`{"page":1}`),
		Work: messages.WorkContext{OperationID: id.New(), TaskID: id.New()}})
	if err != nil {
		t.Fatal(err)
	}
	next := func() messages.Message {
		t.Helper()
		select {
		case result := <-bus.results:
			return result
		case <-ctx.Done():
			t.Fatal("result was not published")
			return messages.Message{}
		}
	}

	// Act: deliver the same command twice, then let automatic result retry run.
	if err := consumer.Consume(ctx, command); err != nil {
		t.Fatal(err)
	}
	first := next()
	if err := consumer.Consume(ctx, command); err != nil {
		t.Fatal(err)
	}
	replay := next()
	finished := make(chan error, 1)
	go func() { finished <- consumer.RetryResults(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("result retry shutdown: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("result retry goroutine did not stop")
		}
	})
	retry := next()

	// Assert: retransmission preserves the result identity, evidence and work
	// context; an altered command cannot reuse the same command ID.
	if runtime.calls != 1 || !reflect.DeepEqual(first, replay) || !reflect.DeepEqual(first, retry) || first.CorrelationID != command.ID || first.Work != command.Work {
		t.Fatalf("execution repeated or result changed: calls=%d first=%+v replay=%+v retry=%+v", runtime.calls, first, replay, retry)
	}
	conflict := command
	conflict.Payload = []byte(`{"action_name":"different"}`)
	if err := consumer.Consume(ctx, conflict); err == nil || runtime.calls != 1 {
		t.Fatal("conflicting command reused the original identity")
	}

	// Act: finally deliver and acknowledge the controller's durable receipt.
	receipt, err := agentevents.NewResultAccepted(first)
	if err != nil {
		t.Fatal(err)
	}
	receipts := consumers.ResultReceiptConsumer{AgentID: consumer.AgentID, Outcomes: outcomes}
	if err := receipts.Consume(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	receipts.AfterAck(receipt)

	// Assert: after acceptance, no further publication or execution is needed.
	select {
	case result := <-bus.results:
		t.Fatalf("accepted result was retried: %+v", result)
	case <-time.After(1200 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

type countingRuntime struct{ calls int }

func (r *countingRuntime) Execute(_ context.Context, call actions.ActionCall, _ string) (actions.ActionOutput, error) {
	r.calls++
	return actions.ActionOutput{ActionName: call.ActionName, Output: []byte(`{"observed":true}`)}, nil
}
func (*countingRuntime) Discover(context.Context, *environment.DiscoveryScope) error      { return nil }
func (*countingRuntime) PublishState(context.Context, messages.MessageType, string) error { return nil }
func (*countingRuntime) Shutdown(context.Context, string) error                           { return nil }

// Capture publication only: receipt delivery is explicitly controlled by the test.
type resultBus struct{ results chan messages.Message }

func (b *resultBus) Publish(ctx context.Context, _ string, message messages.Message) error {
	select {
	case b.results <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*resultBus) Subscribe(context.Context, string) (comms.Subscription, error) {
	return nil, errors.New("subscriptions are unused in this fault test")
}
func (*resultBus) Close() error { return nil }
