package events

import (
	"bytes"
	"errors"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

const (
	SecretTransientResponded = messages.SecretTransientResponded
	TransientSecretInactive  = "inactive"
	TransientSecretExpired   = "expired"
	TransientSecretCancelled = "cancelled"
	TransientSecretConflict  = "conflict"
)

type TransientSecretResponse struct {
	Value []byte `json:"value"`
	Error string `json:"error,omitempty"`
}

func (TransientSecretResponse) String() string   { return "transient secret response (value redacted)" }
func (TransientSecretResponse) GoString() string { return "transient secret response (value redacted)" }

func (response TransientSecretResponse) Validate() error {
	if response.Error == "" {
		if response.Value == nil || len(response.Value) > secrets.MaxTransientSecretBytes {
			return errors.New("transient secret response requires a bounded value")
		}
		return nil
	}
	if response.Value != nil {
		return errors.New("transient secret errors cannot include a value")
	}
	switch response.Error {
	case TransientSecretInactive, TransientSecretExpired, TransientSecretCancelled, TransientSecretConflict:
		return nil
	default:
		return errors.New("invalid transient secret error code")
	}
}

func NewTransientSecretResponse(request secrets.Request, value []byte, code string) (messages.Message, error) {
	if err := request.Validate(); err != nil {
		return messages.Message{}, err
	}
	if request.EffectiveType() != secrets.SecretTransient || request.Status != secrets.Pending {
		return messages.Message{}, errors.New("transient secret response requires its original pending request")
	}
	response := TransientSecretResponse{Value: bytes.Clone(value), Error: code}
	defer clear(response.Value)
	message, err := messages.NewMessage(SecretTransientResponded, request.AgentID, response)
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID, message.ExpiresAt = request.ID, request.ExpiresAt
	message.Work = messages.WorkContext{OperationID: request.OperationID, TaskID: request.TaskID}
	if err := ValidateTransientSecretResponseRequest(message, request); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

// The caller owns the decoded bytes and must clear them after use.
func DecodeTransientSecretResponse(message messages.Message) (TransientSecretResponse, error) {
	if len(message.Payload) > 128*1024 {
		return TransientSecretResponse{}, errors.New("transient secret response exceeds its size limit")
	}
	if err := message.Validate(); err != nil {
		return TransientSecretResponse{}, err
	}
	if message.Type != SecretTransientResponded || !id.Valid(message.CorrelationID) || message.Work.OperationID == "" || message.ExpiresAt.IsZero() {
		return TransientSecretResponse{}, errors.New("transient secret response requires request correlation, execution work and expiry")
	}
	var response TransientSecretResponse
	if err := domain.Decode(message.Payload, &response); err != nil {
		clear(response.Value)
		return TransientSecretResponse{}, errors.New("invalid transient secret response")
	}
	if response.Error == "" && !message.CreatedAt.Before(message.ExpiresAt) {
		clear(response.Value)
		return TransientSecretResponse{}, errors.New("transient value must be delivered before expiry")
	}
	return response, nil
}

func ValidateTransientSecretResponseRequest(message messages.Message, request secrets.Request) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.EffectiveType() != secrets.SecretTransient || request.Status != secrets.Pending {
		return errors.New("transient response must match its original pending request")
	}
	response, err := DecodeTransientSecretResponse(message)
	if err != nil {
		return err
	}
	defer clear(response.Value)
	work := messages.WorkContext{OperationID: request.OperationID, TaskID: request.TaskID}
	if message.AgentID != request.AgentID || message.CorrelationID != request.ID || message.Work != work ||
		!message.ExpiresAt.Equal(request.ExpiresAt) || message.CreatedAt.Before(request.RequestedAt) {
		return errors.New("transient response must match its request identity, recipient, execution and lifetime")
	}
	return nil
}
