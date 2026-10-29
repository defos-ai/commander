package events

import (
	"errors"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

const QuestionRequested = messages.QuestionRequested

type QuestionRequest struct {
	CommandID string   `json:"command_id"`
	Key       string   `json:"key"`
	Text      string   `json:"text"`
	Options   []string `json:"options,omitempty"`
}

func (request QuestionRequest) Validate() error {
	if !id.Valid(request.CommandID) {
		return errors.New("question request requires a command UUID")
	}
	return operations.ValidateExecutionQuestion(request.Key, request.Text, request.Options)
}

func NewQuestionRequest(agentID string, work messages.WorkContext, request QuestionRequest) (messages.Message, error) {
	message, err := messages.NewMessage(QuestionRequested, agentID, request)
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID, message.Work = request.CommandID, work
	if _, err := DecodeQuestionRequest(message); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

func DecodeQuestionRequest(message messages.Message) (QuestionRequest, error) {
	if len(message.Payload) > 128*1024 {
		return QuestionRequest{}, errors.New("question request exceeds its size limit")
	}
	if err := message.Validate(); err != nil {
		return QuestionRequest{}, err
	}
	if message.Type != QuestionRequested || !id.Valid(message.CorrelationID) || !message.ExpiresAt.IsZero() || message.Work.OperationID == "" {
		return QuestionRequest{}, errors.New("question request requires operation work, command correlation and no expiry")
	}
	var request QuestionRequest
	if err := domain.Decode(message.Payload, &request); err != nil {
		return QuestionRequest{}, errors.New("invalid question request")
	}
	if request.CommandID != message.CorrelationID {
		return QuestionRequest{}, errors.New("question request must match its command correlation")
	}
	return request, nil
}
