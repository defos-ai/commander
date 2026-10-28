package events

import (
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

const SecretDelivered = messages.SecretDelivered

// SecretDelivery carries a value over the trusted broker. The envelope ID identifies this
// delivery, and CorrelationID identifies its pending secret request.
type SecretDelivery struct {
	Metadata secretmodel.Metadata `json:"metadata"`
	Value    []byte               `json:"value"`
}

func (delivery SecretDelivery) Validate() error {
	if err := delivery.Metadata.Validate(); err != nil {
		return err
	}
	if delivery.Value == nil {
		return errors.New("secret delivery requires a value")
	}
	return nil
}

func (SecretDelivery) String() string   { return "secret delivery (value redacted)" }
func (SecretDelivery) GoString() string { return "secret delivery (value redacted)" }

// NewSecretDelivery prepares one reply to a pending request. Retain this exact
// message for any retry. It does not publish or fulfill the request.
func NewSecretDelivery(request secretmodel.Request, metadata secretmodel.Metadata, value []byte) (messages.Message, error) {
	payload := SecretDelivery{Metadata: metadata, Value: append([]byte{}, value...)}
	defer clear(payload.Value)
	message, err := messages.NewMessage(SecretDelivered, request.AgentID, payload)
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID = request.ID
	message.ExpiresAt = deliveryDeadline(request, metadata)
	if err := ValidateSecretDeliveryRequest(message, request); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

// DecodeSecretDelivery validates the wire shape, not publisher authorization or
// current readiness. Consumers must also match their active request and check its
// deadline at receipt. The caller owns and must clear the returned value.
func DecodeSecretDelivery(message messages.Message) (SecretDelivery, error) {
	if err := message.Validate(); err != nil {
		return SecretDelivery{}, err
	}
	if message.Type != SecretDelivered || message.Work != (messages.WorkContext{}) || !id.Valid(message.CorrelationID) {
		return SecretDelivery{}, errors.New("secret delivery requires its request correlation and no work context")
	}
	if message.ExpiresAt.IsZero() || !message.ExpiresAt.After(message.CreatedAt) {
		return SecretDelivery{}, errors.New("secret delivery requires a deadline after creation")
	}
	var delivery SecretDelivery
	if err := domain.Decode(message.Payload, &delivery); err != nil {
		clear(delivery.Value)
		return SecretDelivery{}, errors.New("invalid secret delivery")
	}
	metadata := delivery.Metadata
	if metadata.UpdatedAt.After(message.CreatedAt) || secretmodel.AvailabilityAt(&metadata, message.CreatedAt) != secretmodel.Available || (!metadata.ExpiresAt.IsZero() && message.ExpiresAt.After(metadata.ExpiresAt)) {
		clear(delivery.Value)
		return SecretDelivery{}, errors.New("secret delivery must fit the secret validity interval")
	}
	return delivery, nil
}

// ValidateSecretDeliveryRequest binds a delivery to the expected pending request.
// A valid old request is not necessarily the recipient's current active request;
// callers own that check and the current store/version check. These messages do
// not provide unsolicited invalidation or immediate revocation after deletion.
func ValidateSecretDeliveryRequest(message messages.Message, request secretmodel.Request) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.Status != secretmodel.Pending {
		return errors.New("secret delivery requires a pending request")
	}
	delivery, err := DecodeSecretDelivery(message)
	if err != nil {
		return err
	}
	defer clear(delivery.Value)
	if message.AgentID != request.AgentID || message.CorrelationID != request.ID || delivery.Metadata.Key != request.Key {
		return errors.New("secret delivery must match its request identity, recipient and key")
	}
	if message.CreatedAt.Before(request.RequestedAt) || !message.CreatedAt.Before(request.ExpiresAt) || !message.ExpiresAt.Equal(deliveryDeadline(request, delivery.Metadata)) {
		return errors.New("secret delivery must fit its request deadline")
	}
	return nil
}

func deliveryDeadline(request secretmodel.Request, metadata secretmodel.Metadata) time.Time {
	deadline := request.ExpiresAt
	if !metadata.ExpiresAt.IsZero() && metadata.ExpiresAt.Before(deadline) {
		deadline = metadata.ExpiresAt
	}
	return deadline
}
