package clients_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"commander/framework/comms/clients/agent/commands"
	"commander/framework/comms/messages"
	"commander/framework/domain"
	"commander/framework/id"
	fscomms "commander/providers/fs/comms"
)

func TestAgentCommandClientPreservesReservedTaskAcrossBus(t *testing.T) {
	// Arrange
	bus, err := fscomms.NewFsBus(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	agentID := "fixture-agent-1234abcd"
	sub, err := bus.Subscribe(t.Context(), messages.AgentCommands(agentID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	work := messages.WorkContext{OperationID: id.New(), TaskID: id.New(), StepID: "inspect"}
	call := commands.ActionCall{ActionName: "inspect", Input: json.RawMessage(`{"target":"localhost"}`), Work: work}
	prepared, err := commands.NewRunAction(agentID, call)
	if err != nil {
		t.Fatal(err)
	}
	client := commands.Client{Bus: bus}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	// Act
	if err := client.Publish(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	received, err := sub.Next(ctx)
	var decoded commands.ActionCall
	if err == nil {
		err = domain.Decode(received.Payload, &decoded)
	}

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if received.ID != work.TaskID || !reflect.DeepEqual(received, prepared) || !reflect.DeepEqual(decoded, call) {
		t.Fatalf("task identity or command changed: sent %+v; received %+v; decoded %+v", prepared, received, decoded)
	}
}

func TestAgentCommandClientRejectsEventsBeforePublication(t *testing.T) {
	// Arrange
	bus, err := fscomms.NewFsBus(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	agentID := "fixture-agent-1234abcd"
	sub, err := bus.Subscribe(t.Context(), messages.AgentCommands(agentID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	event, err := messages.NewMessage(messages.AgentHeartbeat, agentID, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	client := commands.Client{Bus: bus}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	// Act
	publishErr := client.Publish(ctx, event)
	_, receiveErr := sub.Next(ctx)

	// Assert
	if publishErr == nil || !errors.Is(receiveErr, context.DeadlineExceeded) {
		t.Fatalf("event entered the agent command destination: publish=%v receive=%v", publishErr, receiveErr)
	}
}
