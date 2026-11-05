package fixtures

import (
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/operations"
	controllerfakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	operationfakes "github.com/defos-ai/commander/framework/tests/fakes/operation"
)

func ExecutionQuestionConsumer(t *testing.T) (consumers.EventConsumer, *controllerfakes.CheckpointExecutions, *operationfakes.OperationStore, *controllerfakes.SecretPublishBus) {
	t.Helper()
	consumer, execution, bus := CheckpointConsumer(t)
	command := execution.Snapshot().Command
	deadline := time.Now().Add(time.Hour)
	store := operationfakes.NewOperationStore()
	operation := operations.Operation{ID: command.OperationID, Status: operations.Running,
		Tasks: []operations.Task{{ID: command.TaskID, CommandID: command.ID, OperationID: command.OperationID,
			AgentID: command.AgentID, ActionName: "capture", Status: operations.TaskRunning, DeadlineAt: &deadline}}}
	if err := store.Create(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	consumer.Operations = store
	return consumer, execution, store, bus
}

func ExecutionQuestionRequest(t *testing.T, command messages.Message, key, text string, options ...string) messages.Message {
	t.Helper()
	request, err := controllerevents.NewQuestionRequest(command.AgentID, command.OperationID, command.TaskID, controllerevents.QuestionRequest{
		CommandID: command.ID, Key: key, Text: text, Options: options,
	})
	if err != nil {
		t.Fatal(err)
	}
	return request
}
