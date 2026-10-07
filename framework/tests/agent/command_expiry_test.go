package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/consumers"
	"github.com/defos-ai/commander/framework/agent/environment"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
)

func TestExpiredCommandsDoNotStartRuntimeWork(t *testing.T) {
	for _, kind := range []messages.MessageType{agentcommands.RunAction, agentcommands.DiscoverActions, agentcommands.StopAgent} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, consumer, bus, runtime := commandExpiryFixture(t)
			command := expiryCommand(t, kind, consumer.AgentID)
			command.CreatedAt = time.Now().Add(-time.Minute)
			command.ExpiresAt = command.CreatedAt.Add(time.Second)

			if err := consumer.Consume(ctx, command); err != nil {
				t.Fatalf("expired command was not handled: %v", err)
			}
			result := expiryResult(t, ctx, bus)
			var failure controllerevents.CommandError
			if err := json.Unmarshal(result.Payload, &failure); err != nil {
				t.Fatal(err)
			}
			if result.Type != controllerevents.CommandFailed || result.CorrelationID != command.ID || result.Work != command.Work ||
				failure.Message != "command expired before execution started: "+context.DeadlineExceeded.Error() || !result.ExpiresAt.IsZero() {
				t.Fatalf("expired command has no independent failure outcome: result=%+v failure=%+v", result, failure)
			}
			if calls := runtime.calls.Load(); calls != 0 {
				t.Fatalf("expired command called the runtime %d times", calls)
			}
			if kind == agentcommands.RunAction {
				if err := consumer.Consume(ctx, command); err != nil {
					t.Fatal(err)
				}
				if replay := expiryResult(t, ctx, bus); !reflect.DeepEqual(replay, result) || runtime.calls.Load() != 0 {
					t.Fatalf("expired action replay changed its outcome or started work: %+v", replay)
				}
			}
		})
	}
}

func TestActionAdmissionExpiryPreservesRunningAndCompletedWork(t *testing.T) {
	ctx, consumer, bus, runtime := commandExpiryFixture(t)
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	runtime.started, runtime.release = started, release
	command := expiryCommand(t, agentcommands.RunAction, consumer.AgentID)
	command.ExpiresAt = time.Now().Add(250 * time.Millisecond)
	finished := make(chan error, 1)
	go func() {
		defer close(finished)
		finished <- consumer.Consume(ctx, command)
	}()
	t.Cleanup(func() {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("action consumer did not finish")
		}
	})
	defer func() {
		if release != nil {
			close(release)
		}
	}()
	var execution context.Context
	select {
	case execution = <-started:
	case err := <-finished:
		t.Fatalf("action finished before runtime execution started: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-time.After(time.Until(command.ExpiresAt) + 10*time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := execution.Err(); err != nil {
		t.Fatalf("admission expiry cancelled running work: %v", err)
	}
	select {
	case err := <-finished:
		t.Fatalf("running action finished before release: %v", err)
	default:
	}
	close(release)
	release = nil
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	result := expiryResult(t, ctx, bus)
	if result.Type != controllerevents.ActionSucceeded {
		t.Fatalf("running action did not complete successfully after expiry: %+v", result)
	}
	if err := consumer.Consume(ctx, command); err != nil {
		t.Fatal(err)
	}
	if replay := expiryResult(t, ctx, bus); !reflect.DeepEqual(replay, result) || runtime.calls.Load() != 1 {
		t.Fatalf("expired duplicate changed its saved success or repeated execution: %+v", replay)
	}
}

func TestActionDuplicateCannotChangeItsAdmissionExpiry(t *testing.T) {
	ctx, consumer, bus, runtime := commandExpiryFixture(t)
	command := expiryCommand(t, agentcommands.RunAction, consumer.AgentID)
	command.ExpiresAt = time.Now().Add(time.Minute)
	if err := consumer.Consume(ctx, command); err != nil {
		t.Fatal(err)
	}
	result := expiryResult(t, ctx, bus)
	for _, expiry := range []time.Time{command.ExpiresAt.Add(time.Minute), {}} {
		conflict := command
		conflict.ExpiresAt = expiry
		if err := consumer.Consume(ctx, conflict); err == nil {
			t.Fatalf("duplicate command accepted changed expiry %s", expiry)
		}
		select {
		case result := <-bus.results:
			t.Fatalf("conflicting duplicate published another outcome: %+v", result)
		default:
		}
	}
	if err := consumer.Consume(ctx, command); err != nil {
		t.Fatal(err)
	}
	if replay := expiryResult(t, ctx, bus); !reflect.DeepEqual(replay, result) || runtime.calls.Load() != 1 {
		t.Fatalf("conflicting expiry changed the original outcome or repeated work: %+v", replay)
	}
}

func commandExpiryFixture(t *testing.T) (context.Context, consumers.CommandConsumer, *resultBus, *expiryRuntime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	outcomes := &consumers.Outcomes{}
	done := outcomes.Start(ctx)
	t.Cleanup(func() { cancel(); <-done })
	bus := &resultBus{results: make(chan messages.Message, 8)}
	runtime := &expiryRuntime{}
	agentID := "fixture-agent-12345678"
	events := controllerevents.Client{Bus: bus}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	consumer := consumers.CommandConsumer{
		AgentID:   agentID,
		Action:    consumers.ActionConsumer{AgentID: agentID, Runtime: runtime, Events: events, Logger: logger, Outcomes: outcomes},
		Discovery: consumers.DiscoveryConsumer{AgentID: agentID, Runtime: runtime, Events: events, Logger: logger},
		Stop:      consumers.StopConsumer{AgentID: agentID, Runtime: runtime, Events: events, Logger: logger, Outcomes: outcomes},
	}
	return ctx, consumer, bus, runtime
}

func expiryCommand(t *testing.T, kind messages.MessageType, agentID string) messages.Message {
	t.Helper()
	var command messages.Message
	var err error
	switch kind {
	case agentcommands.RunAction:
		command, err = agentcommands.NewRunAction(agentID, actions.ActionCall{ActionName: "inspect"})
	case agentcommands.DiscoverActions:
		command, err = agentcommands.NewDiscoverActions(agentID, nil)
	case agentcommands.StopAgent:
		command, err = agentcommands.NewStopAgent(agentID)
	default:
		t.Fatalf("unsupported fixture command %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func expiryResult(t *testing.T, ctx context.Context, bus *resultBus) messages.Message {
	t.Helper()
	select {
	case result := <-bus.results:
		return result
	case <-ctx.Done():
		t.Fatal("command result was not published")
		return messages.Message{}
	}
}

type expiryRuntime struct {
	calls   atomic.Int64
	started chan<- context.Context
	release <-chan struct{}
}

func (r *expiryRuntime) Execute(ctx context.Context, call actions.ActionCall, _ string) (actions.ActionOutput, error) {
	r.calls.Add(1)
	if r.started != nil {
		r.started <- ctx
		select {
		case <-r.release:
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
	}
	return actions.ActionOutput{ActionName: call.ActionName, Output: []byte(`{"observed":true}`)}, nil
}

func (r *expiryRuntime) Discover(context.Context, *environment.DiscoveryScope) error {
	r.calls.Add(1)
	return nil
}

func (r *expiryRuntime) PublishState(context.Context, messages.MessageType, string) error {
	r.calls.Add(1)
	return nil
}

func (r *expiryRuntime) Shutdown(context.Context, string) error {
	r.calls.Add(1)
	return nil
}
