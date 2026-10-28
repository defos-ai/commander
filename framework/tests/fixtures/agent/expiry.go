package fixtures

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/consumers"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func CommandExpiry(t *testing.T) (context.Context, consumers.CommandConsumer, *fakes.ResultBus, *fakes.ExpiryRuntime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	outcomes := &consumers.Outcomes{}
	done := outcomes.Start(ctx)
	t.Cleanup(func() { cancel(); <-done })
	bus := fakes.NewResultBus()
	runtime := &fakes.ExpiryRuntime{}
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

func ExpiryCommand(t *testing.T, kind messages.MessageType, agentID string) messages.Message {
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

func ExpiryResult(t *testing.T, ctx context.Context, bus *fakes.ResultBus) messages.Message {
	t.Helper()
	select {
	case result := <-bus.Results():
		return result
	case <-ctx.Done():
		t.Fatal("command result was not published")
		return messages.Message{}
	}
}
