package fixtures

import (
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func QuestionWireRequest(t *testing.T, text string, options []string) messages.Message {
	t.Helper()
	request, err := controllerevents.NewQuestionRequest("fixture-agent-12345678",
		messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()},
		controllerevents.QuestionRequest{CommandID: id.MessageID(), Key: "confirm", Text: text, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func QuestionWireAnswer(t *testing.T, request messages.Message, answer string) operations.Question {
	t.Helper()
	payload, err := controllerevents.DecodeQuestionRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return operations.Question{ID: id.QuestionID(), Key: payload.Key, CommandID: payload.CommandID, TaskID: request.Work.TaskID,
		StepID: request.Work.StepID, Text: payload.Text, Options: payload.Options, AskedAt: now.Add(-time.Minute), FallbackAt: now.Add(time.Minute),
		Status: operations.QuestionAnswered, Answer: answer, AnsweredAt: &now, AnsweredBy: operations.AnswerOperator}
}
