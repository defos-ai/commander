package events

import (
	"errors"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

const (
	SecretTransientReceived = messages.SecretTransientReceived
	SecretRequestCanceled   = messages.SecretRequestCanceled
)

type TransientSecretReceipt struct {
	RequestID string `json:"request_id"`
}

func (receipt TransientSecretReceipt) Validate() error {
	if !id.Valid(receipt.RequestID) {
		return errors.New("transient secret receipt requires a request UUID")
	}
	return nil
}

func NewTransientSecretReceived(delivery messages.Message) (messages.Message, error) {
	payload, err := agentevents.DecodeTransientSecretResponse(delivery)
	if err != nil {
		return messages.Message{}, err
	}
	defer clear(payload.Value)
	if payload.Error != "" {
		return messages.Message{}, errors.New("transient secret receipt requires a value delivery")
	}
	message, err := messages.NewMessage(SecretTransientReceived, delivery.AgentID, TransientSecretReceipt{RequestID: delivery.CorrelationID})
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID = delivery.ID
	message.OperationID, message.TaskID = delivery.OperationID, delivery.TaskID
	if err := ValidateTransientSecretReceivedDelivery(message, delivery); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

func DecodeTransientSecretReceived(message messages.Message) (TransientSecretReceipt, error) {
	if len(message.Payload) > 1024 {
		return TransientSecretReceipt{}, errors.New("transient secret receipt exceeds its size limit")
	}
	if err := message.Validate(); err != nil {
		return TransientSecretReceipt{}, err
	}
	if message.Type != SecretTransientReceived || !id.Valid(message.CorrelationID) || message.OperationID == "" || !message.ExpiresAt.IsZero() {
		return TransientSecretReceipt{}, errors.New("transient secret receipt requires delivery correlation, execution work and no expiry")
	}
	var receipt TransientSecretReceipt
	if err := domain.Decode(message.Payload, &receipt); err != nil {
		return TransientSecretReceipt{}, errors.New("invalid transient secret receipt")
	}
	return receipt, nil
}

func ValidateTransientSecretReceivedDelivery(message, delivery messages.Message) error {
	receipt, err := DecodeTransientSecretReceived(message)
	if err != nil {
		return err
	}
	payload, err := agentevents.DecodeTransientSecretResponse(delivery)
	if err != nil {
		return err
	}
	defer clear(payload.Value)
	if payload.Error != "" || receipt.RequestID != delivery.CorrelationID || message.CorrelationID != delivery.ID ||
		message.AgentID != delivery.AgentID || message.OperationID != delivery.OperationID || message.TaskID != delivery.TaskID || message.CreatedAt.Before(delivery.CreatedAt) ||
		!message.CreatedAt.Before(delivery.ExpiresAt) {
		return errors.New("transient secret receipt must match its exact valid delivery")
	}
	return nil
}

type SecretRequestCancellation struct {
	Request secrets.Request `json:"request"`
}

func (cancellation SecretRequestCancellation) Validate() error {
	if err := cancellation.Request.Validate(); err != nil {
		return err
	}
	if cancellation.Request.EffectiveType() != secrets.SecretTransient || cancellation.Request.Status != secrets.Pending {
		return errors.New("secret cancellation requires the original pending transient request")
	}
	return nil
}

func NewSecretRequestCanceled(request secrets.Request) (messages.Message, error) {
	message, err := messages.NewMessage(SecretRequestCanceled, request.AgentID, SecretRequestCancellation{Request: request})
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID, message.ExpiresAt = request.ID, request.ExpiresAt
	message.OperationID, message.TaskID = request.OperationID, request.TaskID
	if _, err := DecodeSecretRequestCanceled(message); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

func DecodeSecretRequestCanceled(message messages.Message) (SecretRequestCancellation, error) {
	if len(message.Payload) > 16*1024 {
		return SecretRequestCancellation{}, errors.New("secret cancellation exceeds its size limit")
	}
	if err := message.Validate(); err != nil {
		return SecretRequestCancellation{}, err
	}
	if message.Type != SecretRequestCanceled {
		return SecretRequestCancellation{}, errors.New("invalid secret cancellation type")
	}
	var cancellation SecretRequestCancellation
	if err := domain.Decode(message.Payload, &cancellation); err != nil {
		return SecretRequestCancellation{}, errors.New("invalid secret cancellation")
	}
	request := cancellation.Request
	if message.AgentID != request.AgentID || message.CorrelationID != request.ID ||
		message.OperationID != request.OperationID || message.TaskID != request.TaskID ||
		!message.ExpiresAt.Equal(request.ExpiresAt) || message.CreatedAt.Before(request.RequestedAt) {
		return SecretRequestCancellation{}, errors.New("secret cancellation must match its original request")
	}
	return cancellation, nil
}
