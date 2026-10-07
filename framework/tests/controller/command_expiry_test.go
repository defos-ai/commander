package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
)

func TestControllerSetsAgentCommandExpiry(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		name := "default admission window"
		if explicit {
			name = "caller deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctx, c, bus, _ := fixture(t, time.Hour)
			caller, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			deadline := time.Now().Add(3 * time.Second)
			if explicit {
				var stop context.CancelFunc
				caller, stop = context.WithDeadline(caller, deadline)
				t.Cleanup(stop)
			}

			finished := discover(caller, c)
			command := receive(t, ctx, bus.commands)

			if !explicit {
				deadline = command.CreatedAt.Add(5 * time.Minute)
			}
			if !command.ExpiresAt.Equal(deadline) {
				t.Fatalf("command expiry = %s, want %s", command.ExpiresAt, deadline)
			}
			bus.observe(t, ctx, controllerevents.AgentUpdated, command.ID)
			if err := receive(t, ctx, finished); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControllerRedeliversOriginalExpiry(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command, _, _ := artifactResult(t)
	command.CreatedAt = time.Now().UTC().Add(-2 * time.Minute)
	command.ExpiresAt = command.CreatedAt.Add(time.Minute)
	durable := savedCommand{record: executions.Execution{Command: command, StartedAt: command.CreatedAt}}
	bus := artifactBus()
	c, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: &inventory{}, ActionStore: unusedActions{}, ExecutionStore: durable, ArtifactStore: unusedArtifacts{},
		HeartbeatTimeout: time.Hour, LeaseSweepInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closing, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := c.Close(closing); err != nil {
			t.Error(err)
		}
	})
	bus.observe(t, ctx, controllerevents.AgentStarted, "")
	var call actions.ActionCall
	if err := json.Unmarshal(command.Payload, &call); err != nil {
		t.Fatal(err)
	}
	caller, stop := context.WithCancel(ctx)
	defer stop()
	finished := make(chan error, 1)

	go func() { _, err := c.DispatchAction(caller, agentID, call); finished <- err }()
	replayed := receive(t, ctx, bus.commands)

	if !reflect.DeepEqual(replayed, command) {
		t.Fatalf("recovery extended expiry or changed saved command: %+v", replayed)
	}
	stop()
	if err := receive(t, ctx, finished); !errors.Is(err, context.Canceled) {
		t.Fatalf("recovery wait did not stop: %v", err)
	}
}

type savedCommand struct {
	executions.ExecutionStore
	record executions.Execution
}

func (s savedCommand) Get(context.Context, string, string) (executions.Execution, error) {
	return s.record, nil
}

func (s savedCommand) Claim(context.Context, messages.Message) (executions.Execution, bool, error) {
	return s.record, false, nil
}
