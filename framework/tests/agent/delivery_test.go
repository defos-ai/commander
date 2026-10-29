package agent_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/consumers"
	"github.com/defos-ai/commander/framework/comms"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func TestDuplicateCommandAndMissingReceiptDoNotRepeatExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	bus := fakes.NewResultBus()
	runtime := &fakes.CountingRuntime{}
	outcomes := &consumers.Outcomes{}
	outcomesDone := outcomes.Start(ctx)
	t.Cleanup(func() { cancel(); <-outcomesDone })
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
		case result := <-bus.Results():
			return result
		case <-ctx.Done():
			t.Fatal("result was not published")
			return messages.Message{}
		}
	}

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

	if runtime.Calls() != 1 || !reflect.DeepEqual(first, replay) || !reflect.DeepEqual(first, retry) || first.CorrelationID != command.ID || first.Work != command.Work {
		t.Fatalf("execution repeated or result changed: calls=%d first=%+v replay=%+v retry=%+v", runtime.Calls(), first, replay, retry)
	}
	conflict := command
	conflict.Payload = []byte(`{"action_name":"different"}`)
	if err := consumer.Consume(ctx, conflict); err == nil || runtime.Calls() != 1 {
		t.Fatal("conflicting command reused the original identity")
	}

	receipt, err := agentevents.NewResultAccepted(first)
	if err != nil {
		t.Fatal(err)
	}
	receipts := consumers.ResultReceiptConsumer{AgentID: consumer.AgentID, Outcomes: outcomes}
	if err := receipts.Consume(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if err := receipts.AfterAck(ctx, receipt); err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-bus.Results():
		t.Fatalf("accepted result was retried: %+v", result)
	case <-time.After(1200 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestFailedPublicationWaitsForReceiptAndPreservesOutcomeAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	lifetime, stop := context.WithCancel(ctx)
	outcomes := &consumers.Outcomes{}
	outcomesDone := outcomes.Start(lifetime)
	bus := fakes.NewFlakyResultBus()
	runtime := &fakes.CountingRuntime{}
	consumer := consumers.ActionConsumer{Runtime: runtime, AgentID: "fixture-agent-12345678", Outcomes: outcomes,
		Events: controllerevents.Client{Bus: bus}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	command, err := agentcommands.NewRunAction(consumer.AgentID, actions.ActionCall{ActionName: "inspect", Input: []byte(`{"page":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	finished, retried := make(chan error, 1), make(chan error, 1)
	go func() { finished <- consumer.Consume(lifetime, command) }()
	go func() { retried <- consumer.RetryResults(lifetime) }()
	t.Cleanup(func() { stop(); <-outcomesDone })

	var result messages.Message
	select {
	case result = <-bus.Results():
	case <-ctx.Done():
		t.Fatal("failed publication was not retried")
	}
	select {
	case err := <-finished:
		t.Fatalf("command stopped waiting before its receipt: %v", err)
	default:
	}
	receipt, err := agentevents.NewResultAccepted(result)
	if err != nil {
		t.Fatal(err)
	}
	receipts := consumers.ResultReceiptConsumer{AgentID: consumer.AgentID, Outcomes: outcomes}
	if err := receipts.Consume(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if err := receipts.AfterAck(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("receipt did not release the waiting command")
	}
	stop()
	if err := <-retried; !errors.Is(err, context.Canceled) {
		t.Fatalf("retry worker failed to stop: %v", err)
	}
	<-outcomesDone

	if err := consumer.Consume(ctx, command); !errors.Is(err, comms.ErrClosed) {
		t.Fatalf("stopped outcomes accepted a command: %v", err)
	}
	restarted, stopRestart := context.WithCancel(ctx)
	restartDone := outcomes.Start(restarted)
	t.Cleanup(func() { stopRestart(); <-restartDone })
	if err := consumer.Consume(ctx, command); err != nil {
		t.Fatal(err)
	}
	select {
	case replay := <-bus.Results():
		if runtime.Calls() != 1 || !reflect.DeepEqual(result, replay) {
			t.Fatalf("restart repeated work or changed its result: calls=%d replay=%+v", runtime.Calls(), replay)
		}
	case <-ctx.Done():
		t.Fatal("restart did not replay the saved outcome")
	}
}
