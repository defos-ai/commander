package events

import (
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
)

const ExecutionLogged = messages.ExecutionLogged

const maxLogPayloadSize = 64 * 1024

func NewExecutionLog(log executions.Log) (messages.Message, error) {
	if err := log.Validate(); err != nil {
		return messages.Message{}, err
	}
	payload, err := json.Marshal(log)
	if err != nil {
		return messages.Message{}, err
	}
	message := messages.Message{
		ID: log.ID, Type: ExecutionLogged, AgentID: log.AgentID,
		CorrelationID: log.CommandID, Work: log.Work, Payload: payload, CreatedAt: log.CreatedAt,
	}
	if _, err := DecodeExecutionLog(message); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

// Wire identities must agree; the controller separately verifies the saved claim.
func DecodeExecutionLog(message messages.Message) (executions.Log, error) {
	if len(message.Payload) > maxLogPayloadSize {
		return executions.Log{}, errors.New("execution log payload exceeds its size limit")
	}
	if err := message.Validate(); err != nil {
		return executions.Log{}, err
	}
	if message.Type != ExecutionLogged || !message.ExpiresAt.IsZero() {
		return executions.Log{}, errors.New("execution log requires a log event without expiry")
	}
	var log executions.Log
	if !utf8.Valid(message.Payload) || domain.Decode(message.Payload, &log) != nil {
		return executions.Log{}, errors.New("invalid execution log payload")
	}
	if log.ID != message.ID || log.AgentID != message.AgentID || log.CommandID != message.CorrelationID ||
		log.Work != message.Work || !log.CreatedAt.Equal(message.CreatedAt) {
		return executions.Log{}, errors.New("execution log does not match its envelope")
	}
	return log, nil
}
