package fixtures

import (
	"testing"

	"github.com/defos-ai/commander/framework/controller/consumers"
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

func TransientSecretConsumer(t *testing.T) TransientSecrets {
	t.Helper()
	consumer, requests, values, bus := SecretConsumer(t)
	questions, execution, operations, _ := ExecutionQuestionConsumer(t)
	consumer.Executions, consumer.Operations = execution, questions.Operations
	command := execution.Snapshot().Command
	request := TransientSecretRequest()
	request.OperationID, request.TaskID, request.CommandID = command.Work.OperationID, command.Work.TaskID, command.ID
	t.Cleanup(consumer.ClearTransient)
	return TransientSecrets{Consumer: &consumer, Requests: requests, Values: values, Bus: bus,
		Executions: execution, Operations: operations, Request: request}
}
