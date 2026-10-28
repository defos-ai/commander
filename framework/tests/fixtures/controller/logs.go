package fixtures

import (
	"context"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/internal/state"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
)

type LogConsumer struct {
	Consumer   consumers.EventConsumer
	Executions *fakes.LoggedExecutions
	Command    messages.Message
	Result     messages.Message
	Log        executions.Log
}

func NewLogConsumer(t *testing.T) LogConsumer {
	t.Helper()
	command, result, output := ArtifactResult(t)
	stored := fakes.NewLoggedExecutions(executions.Execution{Command: command, StartedAt: command.CreatedAt})
	return LogConsumer{
		Consumer:   consumers.EventConsumer{Executions: stored, State: state.New(t.Context(), time.Hour)},
		Executions: stored, Command: command, Result: result,
		Log: executions.Log{ID: id.New(), AgentID: command.AgentID, CommandID: command.ID, ActionName: output.ActionName,
			Work: command.Work, Message: "  Preparing source\n", CreatedAt: time.Now().UTC()},
	}
}

func LogMessage(t *testing.T, log executions.Log) messages.Message {
	t.Helper()
	message, err := controllerevents.NewExecutionLog(log)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func StartLogController(t *testing.T, executionStore executions.ExecutionStore) (context.Context, *controller.Controller, *fakes.Bus, *fakes.Inventory) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	bus := fakes.NewBus()
	inventory := &fakes.Inventory{}
	c, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: inventory, ActionStore: fakes.UnusedActions{}, ArtifactStore: fakes.UnusedArtifacts{},
		ExecutionStore:   executionStore,
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
	return ctx, c, bus, inventory
}
