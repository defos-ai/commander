package agent_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestExpiredCommandsDoNotStartRuntimeWork(t *testing.T) {
	for _, kind := range []messages.MessageType{agentcommands.RunAction, agentcommands.DiscoverActions, agentcommands.StopAgent} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, consumer, bus, runtime := fixtures.CommandExpiry(t)
			command := fixtures.ExpiryCommand(t, kind, consumer.AgentID)
			command.CreatedAt = time.Now().Add(-time.Minute)
			command.ExpiresAt = command.CreatedAt.Add(time.Second)

			if err := consumer.Consume(ctx, command); err != nil {
				t.Fatalf("expired command was not handled: %v", err)
			}
			result := fixtures.ExpiryResult(t, ctx, bus)
			var failure controllerevents.CommandError
			if err := json.Unmarshal(result.Payload, &failure); err != nil {
				t.Fatal(err)
			}
			if result.Type != controllerevents.CommandFailed || result.CorrelationID != command.ID || result.Work != command.Work ||
				failure.Message != "command expired before execution started: "+context.DeadlineExceeded.Error() || !result.ExpiresAt.IsZero() {
				t.Fatalf("expired command has no independent failure outcome: result=%+v failure=%+v", result, failure)
			}
			if calls := runtime.Calls(); calls != 0 {
				t.Fatalf("expired command called the runtime %d times", calls)
			}
			if kind == agentcommands.RunAction {
				if err := consumer.Consume(ctx, command); err != nil {
					t.Fatal(err)
				}
				if replay := fixtures.ExpiryResult(t, ctx, bus); !reflect.DeepEqual(replay, result) || runtime.Calls() != 0 {
					t.Fatalf("expired action replay changed its outcome or started work: %+v", replay)
				}
			}
		})
	}
}

func TestActionAdmissionExpiryPreservesRunningAndCompletedWork(t *testing.T) {
	ctx, consumer, bus, runtime := fixtures.CommandExpiry(t)
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	runtime.Pause(started, release)
	command := fixtures.ExpiryCommand(t, agentcommands.RunAction, consumer.AgentID)
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
	result := fixtures.ExpiryResult(t, ctx, bus)
	if result.Type != controllerevents.ActionSucceeded {
		t.Fatalf("running action did not complete successfully after expiry: %+v", result)
	}
	if err := consumer.Consume(ctx, command); err != nil {
		t.Fatal(err)
	}
	if replay := fixtures.ExpiryResult(t, ctx, bus); !reflect.DeepEqual(replay, result) || runtime.Calls() != 1 {
		t.Fatalf("expired duplicate changed its saved success or repeated execution: %+v", replay)
	}
}

func TestActionDuplicateCannotChangeItsAdmissionExpiry(t *testing.T) {
	ctx, consumer, bus, runtime := fixtures.CommandExpiry(t)
	command := fixtures.ExpiryCommand(t, agentcommands.RunAction, consumer.AgentID)
	command.ExpiresAt = time.Now().Add(time.Minute)
	if err := consumer.Consume(ctx, command); err != nil {
		t.Fatal(err)
	}
	result := fixtures.ExpiryResult(t, ctx, bus)
	for _, expiry := range []time.Time{command.ExpiresAt.Add(time.Minute), {}} {
		conflict := command
		conflict.ExpiresAt = expiry
		if err := consumer.Consume(ctx, conflict); err == nil {
			t.Fatalf("duplicate command accepted changed expiry %s", expiry)
		}
		select {
		case result := <-bus.Results():
			t.Fatalf("conflicting duplicate published another outcome: %+v", result)
		default:
		}
	}
	if err := consumer.Consume(ctx, command); err != nil {
		t.Fatal(err)
	}
	if replay := fixtures.ExpiryResult(t, ctx, bus); !reflect.DeepEqual(replay, result) || runtime.Calls() != 1 {
		t.Fatalf("conflicting expiry changed the original outcome or repeated work: %+v", replay)
	}
}
