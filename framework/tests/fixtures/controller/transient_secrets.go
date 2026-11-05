package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/secrets"
	controllerfakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	operationfakes "github.com/defos-ai/commander/framework/tests/fakes/operation"
)

type TransientSecrets struct {
	Consumer   *consumers.SecretEventConsumer
	Requests   *controllerfakes.SecretRequests
	Values     *controllerfakes.SecretValues
	Bus        *controllerfakes.SecretPublishBus
	Executions *controllerfakes.CheckpointExecutions
	Operations *operationfakes.OperationStore
	Request    secrets.Request
}

func (fixture TransientSecrets) CompleteExecution(t *testing.T) {
	t.Helper()
	command := fixture.Executions.Snapshot().Command
	_, result, output := ArtifactResult(t)
	result.AgentID, result.CorrelationID, result.OperationID, result.TaskID = command.AgentID, command.ID, command.OperationID, command.TaskID
	now := time.Now().UTC()
	fixture.Executions.Change(func(record *executions.Execution) {
		record.Result, record.CompletedAt = &result, &now
	})
	_, err := fixture.Operations.Update(t.Context(), command.OperationID, func(operation *operations.Operation) error {
		for index := range operation.Tasks {
			task := &operation.Tasks[index]
			if task.ID == command.TaskID {
				task.Status, task.Result, task.FinishedAt = operations.TaskSucceeded, &output, &now
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TransientSecretConsumer(t *testing.T) TransientSecrets {
	t.Helper()
	consumer, requests, values, bus := SecretConsumer(t)
	questions, execution, operations, _ := ExecutionQuestionConsumer(t)
	consumer.Executions, consumer.Operations = execution, questions.Operations
	command := execution.Snapshot().Command
	request := TransientSecretRequest()
	request.OperationID, request.TaskID, request.CommandID = command.OperationID, command.TaskID, command.ID
	t.Cleanup(consumer.ClearTransient)
	return TransientSecrets{Consumer: &consumer, Requests: requests, Values: values, Bus: bus,
		Executions: execution, Operations: operations, Request: request}
}
