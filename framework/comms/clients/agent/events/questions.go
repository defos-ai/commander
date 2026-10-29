package events

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

const (
	QuestionResponded = messages.QuestionResponded
	QuestionRejected  = "rejected"
	QuestionInactive  = "inactive"
	QuestionConflict  = "conflict"
)

type QuestionResponse struct {
	CommandID string               `json:"command_id"`
	Key       string               `json:"key"`
	Question  *operations.Question `json:"question,omitempty"`
	Error     string               `json:"error,omitempty"`
}

func (response QuestionResponse) Validate() error {
	if !id.Valid(response.CommandID) {
		return errors.New("question response requires a command UUID")
	}
	if err := operations.ValidateExecutionQuestion(response.Key, "Question", nil); err != nil {
		return err
	}
	if response.Error != "" {
		if response.Question != nil {
			return errors.New("question errors must not include a question")
		}
		switch response.Error {
		case QuestionRejected, QuestionInactive, QuestionConflict:
			return nil
		default:
			return errors.New("invalid question error code")
		}
	}
	q := response.Question
	if q == nil || !id.Valid(q.ID) || q.CommandID != response.CommandID || q.Key != response.Key || !id.Valid(q.TaskID) ||
		q.AskedAt.IsZero() || !q.FallbackAt.After(q.AskedAt) {
		return errors.New("question response requires its saved execution question")
	}
	if err := operations.ValidateExecutionQuestion(q.Key, q.Text, q.Options); err != nil {
		return err
	}
	switch q.Status {
	case operations.QuestionPending:
		if q.Answer != "" || q.AnsweredAt != nil || q.AnsweredBy != "" {
			return errors.New("pending question must not contain an answer")
		}
	case operations.QuestionAnswered:
		if strings.TrimSpace(q.Answer) == "" || q.AnsweredAt == nil || q.AnsweredAt.Before(q.AskedAt) || q.AnsweredBy.Validate() != nil {
			return errors.New("answered question requires its saved answer and origin")
		}
		if q.AnsweredBy == operations.AnswerModel && q.AnsweredAt.Before(q.FallbackAt) {
			return errors.New("model answer precedes operator-first deadline")
		}
	default:
		return errors.New("invalid question status")
	}
	return nil
}

func NewQuestionResponse(request messages.Message, response QuestionResponse) (messages.Message, error) {
	if err := request.Validate(); err != nil {
		return messages.Message{}, err
	}
	if request.Type != messages.QuestionRequested || request.CorrelationID != response.CommandID || !request.ExpiresAt.IsZero() || request.Work.OperationID == "" {
		return messages.Message{}, errors.New("question response must match its request command and operation")
	}
	var identity struct {
		CommandID string `json:"command_id"`
		Key       string `json:"key"`
	}
	if err := json.Unmarshal(request.Payload, &identity); err != nil || identity.CommandID != response.CommandID || identity.Key != response.Key {
		return messages.Message{}, errors.New("question response must match its request key and command")
	}
	message, err := messages.NewMessage(QuestionResponded, request.AgentID, response)
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID, message.Work = request.ID, request.Work
	if _, err := DecodeQuestionResponse(message); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

func DecodeQuestionResponse(message messages.Message) (QuestionResponse, error) {
	if len(message.Payload) > 256*1024 {
		return QuestionResponse{}, errors.New("question response exceeds its size limit")
	}
	if err := message.Validate(); err != nil {
		return QuestionResponse{}, err
	}
	if message.Type != QuestionResponded || !id.Valid(message.CorrelationID) || !message.ExpiresAt.IsZero() || message.Work.OperationID == "" {
		return QuestionResponse{}, errors.New("question response requires operation work, request correlation and no expiry")
	}
	var response QuestionResponse
	if err := domain.Decode(message.Payload, &response); err != nil {
		return QuestionResponse{}, errors.New("invalid question response")
	}
	if response.Question != nil && response.Question.TaskID != message.Work.TaskID {
		return QuestionResponse{}, errors.New("question response must match its execution work")
	}
	return response, nil
}
