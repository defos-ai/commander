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
	CheckpointResponded        = messages.CheckpointResponded
	CheckpointNotFound         = "not_found"
	CheckpointUnknownExecution = "unknown_execution"
	CheckpointConflict         = "conflict"
	CheckpointCompleted        = "completed"
	CheckpointInvalid          = "invalid_checkpoint"
)

// CheckpointResponse confirms one read or durable save. Errors contain fixed
// codes and no checkpoint data. Its envelope correlates to the request message.
type CheckpointResponse struct {
	CommandID string          `json:"command_id"`
	Name      string          `json:"name"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func (response CheckpointResponse) Validate() error {
	if !id.Valid(response.CommandID) {
		return errors.New("checkpoint response requires a command UUID")
	}
	if response.Error == "" {
		return executions.ValidateCheckpoint(response.Name, response.Data)
	}
	if len(response.Data) != 0 {
		return errors.New("checkpoint errors must not include data")
	}
	switch response.Error {
	case CheckpointNotFound, CheckpointUnknownExecution, CheckpointConflict, CheckpointCompleted, CheckpointInvalid:
	default:
		return errors.New("invalid checkpoint error code")
	}
	return executions.ValidateCheckpoint(response.Name, json.RawMessage(`{}`))
}

// NewCheckpointResponse prepares a reply after the controller has validated the
// request and persisted any save. Construction alone does not confirm durability.
func NewCheckpointResponse(request messages.Message, response CheckpointResponse) (messages.Message, error) {
	if err := request.Validate(); err != nil {
		return messages.Message{}, err
	}
	if request.Type != messages.CheckpointRequested || request.CorrelationID != response.CommandID || !request.ExpiresAt.IsZero() {
		return messages.Message{}, errors.New("checkpoint response must match its request command")
	}
	var identity struct {
		CommandID string `json:"command_id"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(request.Payload, &identity); err != nil || identity.CommandID != response.CommandID || identity.Name != response.Name {
		return messages.Message{}, errors.New("checkpoint response must match its request name and command")
	}
	message, err := messages.NewMessage(CheckpointResponded, request.AgentID, response)
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID, message.Work = request.ID, request.Work
	if _, err := DecodeCheckpointResponse(message); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

func DecodeCheckpointResponse(message messages.Message) (CheckpointResponse, error) {
	if len(message.Payload) > executions.MaxCheckpointSize+512 {
		return CheckpointResponse{}, errors.New("checkpoint response exceeds its size limit")
	}
	if err := message.Validate(); err != nil {
		return CheckpointResponse{}, err
	}
	if message.Type != CheckpointResponded || !id.Valid(message.CorrelationID) || !message.ExpiresAt.IsZero() {
		return CheckpointResponse{}, errors.New("checkpoint response requires its request correlation and no expiry")
	}
	var response CheckpointResponse
	if err := domain.Decode(message.Payload, &response); err != nil {
		return CheckpointResponse{}, errors.New("invalid checkpoint response")
	}
	return response, nil
}
