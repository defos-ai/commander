package comms_test

import (
	"strings"
	"testing"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/comms"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/comms"
)

func TestExecutionQuestionWireSupportsEscapedContentAtDomainBounds(t *testing.T) {
	text := strings.Repeat("<", 8192)
	options := []string{strings.Repeat("<", 1024), strings.Repeat(">", 1024), strings.Repeat("&", 1024), strings.Repeat("\x00", 1024)}
	request := fixtures.QuestionWireRequest(t, text, options)
	question := fixtures.QuestionWireAnswer(t, request, strings.Repeat("&", operations.MaxExecutionQuestionAnswerBytes))
	response, err := agentevents.NewQuestionResponse(request, agentevents.QuestionResponse{
		CommandID: question.CommandID, Key: question.Key, Question: &question,
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := &fakes.SecretBus{}

	if err := (controllerevents.Client{Bus: bus}).Publish(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := (agentevents.Client{Bus: bus}).Publish(t.Context(), response); err != nil {
		t.Fatal(err)
	}

	decoded, err := agentevents.DecodeQuestionResponse(response)
	if err != nil || decoded.Question.Answer != question.Answer || decoded.Question.Text != text || response.CorrelationID != request.ID ||
		response.Work != request.Work || response.AgentID != request.AgentID {
		t.Fatalf("question exchange lost valid bounded content or request identity: %v", err)
	}
	if topics := bus.Topics(); len(topics) != 2 || topics[0] != messages.ControllerEvents(messages.DefaultControllerType) || topics[1] != messages.AgentEvents(request.AgentID) {
		t.Fatal("question exchange used the action command inbox")
	}
}
