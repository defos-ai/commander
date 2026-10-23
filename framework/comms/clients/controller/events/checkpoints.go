package events

import (
	"encoding/json"
	"errors"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
)

const (
	CheckpointRequested = messages.CheckpointRequested
	CheckpointLoad      = "load"
	CheckpointSave      = "save"
)

// CheckpointRequest reads or appends one fact within an existing execution.
// Its envelope correlates to the original action command, not another request.
type CheckpointRequest struct {
	CommandID string          `json:"command_id"`
	Name      string          `json:"name"`
	Operation string          `json:"operation"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func (request CheckpointRequest) Validate() error {
	if !id.Valid(request.CommandID) {
		return errors.New("checkpoint request requires a command UUID")
	}
	switch request.Operation {
	case CheckpointLoad:
		if len(request.Data) != 0 {
			return errors.New("checkpoint reads must not include data")
		}
		return executions.ValidateCheckpoint(request.Name, json.RawMessage(`{}`))
	case CheckpointSave:
		return executions.ValidateCheckpoint(request.Name, request.Data)
	default:
		return errors.New("checkpoint operation must be load or save")
	}
}

func NewCheckpointRequest(agentID string, work messages.WorkContext, request CheckpointRequest) (messages.Message, error) {
	message, err := messages.NewMessage(CheckpointRequested, agentID, request)
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID, message.Work = request.CommandID, work
	if _, err := DecodeCheckpointRequest(message); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

// DecodeCheckpointRequest validates wire shape only. The controller must match
// the agent and work against its saved command before reading or writing data.
func DecodeCheckpointRequest(message messages.Message) (CheckpointRequest, error) {
	if len(message.Payload) > executions.MaxCheckpointSize+512 {
		return CheckpointRequest{}, errors.New("checkpoint request exceeds its size limit")
	}
	if err := message.Validate(); err != nil {
		return CheckpointRequest{}, err
	}
	if message.Type != CheckpointRequested || !id.Valid(message.CorrelationID) || !message.ExpiresAt.IsZero() {
		return CheckpointRequest{}, errors.New("checkpoint request requires its command correlation and no expiry")
	}
	var request CheckpointRequest
	if err := domain.Decode(message.Payload, &request); err != nil {
		return CheckpointRequest{}, errors.New("invalid checkpoint request")
	}
	if request.CommandID != message.CorrelationID {
		return CheckpointRequest{}, errors.New("checkpoint request must match its command correlation")
	}
	return request, nil
}
