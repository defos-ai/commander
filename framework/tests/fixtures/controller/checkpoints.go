package fixtures

import (
	"encoding/json"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/internal/state"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
)

func CheckpointConsumer(t *testing.T) (consumers.EventConsumer, *fakes.CheckpointExecutions, *fakes.SecretPublishBus) {
	t.Helper()
	command, _, _ := ArtifactResult(t)
	store := fakes.NewCheckpointExecutions(executions.Execution{Command: command, StartedAt: command.CreatedAt})
	bus := &fakes.SecretPublishBus{}
	return consumers.EventConsumer{Executions: store, Bus: bus, State: state.New(t.Context(), time.Hour)}, store, bus
}

func CheckpointRequest(t *testing.T, command messages.Message, name, operation, data string) messages.Message {
	t.Helper()
	request, err := controllerevents.NewCheckpointRequest(command.AgentID, command.Work,
		controllerevents.CheckpointRequest{CommandID: command.ID, Name: name, Operation: operation, Data: json.RawMessage(data)})
	if err != nil {
		t.Fatal(err)
	}
	return request
}
