package events

import (
	"errors"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

const (
	SecretSyncRequested = messages.SecretSyncRequested
	SecretInstalled     = messages.SecretInstalled
)

// SecretSyncRequest asks for one shared secret independently of operations.
type SecretSyncRequest struct {
	Request secretmodel.Request `json:"request"`
}

func (sync SecretSyncRequest) Validate() error {
	if err := sync.Request.Validate(); err != nil {
		return err
	}
	if sync.Request.Status != secretmodel.Pending {
		return errors.New("secret synchronization requires a pending request")
	}
	return nil
}

// SecretInstallation reports successful installation, not broker receipt.
// CorrelationID in its envelope is the original delivery's ID.
type SecretInstallation struct {
	RequestID     string `json:"request_id"`
	SecretID      string `json:"secret_id"`
	SecretVersion uint64 `json:"secret_version"`
}

func (installation SecretInstallation) Validate() error {
	if !id.Valid(installation.RequestID) || !id.Valid(installation.SecretID) || installation.SecretVersion == 0 {
		return errors.New("secret installation requires request and secret identities and a positive version")
	}
	return nil
}

func NewSecretSyncRequested(request secretmodel.Request) (messages.Message, error) {
	message, err := messages.NewMessage(SecretSyncRequested, request.AgentID, SecretSyncRequest{Request: request})
	if err != nil {
		return messages.Message{}, err
	}
	message.ID, message.CreatedAt, message.ExpiresAt = request.ID, request.RequestedAt, request.ExpiresAt
	if _, err := DecodeSecretSyncRequested(message); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

// DecodeSecretSyncRequested validates a request's shape and source fields.
// It does not prove sender identity or establish recipient authorization.
func DecodeSecretSyncRequested(message messages.Message) (SecretSyncRequest, error) {
	if err := message.Validate(); err != nil {
		return SecretSyncRequest{}, err
	}
	if message.Type != SecretSyncRequested || message.Work != (messages.WorkContext{}) || message.CorrelationID != "" {
		return SecretSyncRequest{}, errors.New("secret synchronization requires no work or correlation context")
	}
	var sync SecretSyncRequest
	if err := domain.Decode(message.Payload, &sync); err != nil {
		return SecretSyncRequest{}, errors.New("invalid secret synchronization request")
	}
	request := sync.Request
	if message.ID != request.ID || message.AgentID != request.AgentID || !message.CreatedAt.Equal(request.RequestedAt) || !message.ExpiresAt.Equal(request.ExpiresAt) {
		return SecretSyncRequest{}, errors.New("secret synchronization envelope must match its request")
	}
	return sync, nil
}

// NewSecretInstalled is called only after validating and installing a
// delivery. Creating or publishing this acknowledgement does not fulfill the
// request; the controller must verify its current saved identity/version first.
func NewSecretInstalled(delivery messages.Message) (messages.Message, error) {
	payload, err := agentevents.DecodeSecretDelivery(delivery)
	if err != nil {
		return messages.Message{}, err
	}
	defer clear(payload.Value)
	metadata := payload.Metadata
	message, err := messages.NewMessage(SecretInstalled, delivery.AgentID, SecretInstallation{
		RequestID: delivery.CorrelationID, SecretID: metadata.ID, SecretVersion: metadata.Version,
	})
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID = delivery.ID
	if err := ValidateSecretInstalledDelivery(message, delivery); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

func DecodeSecretInstalled(message messages.Message) (SecretInstallation, error) {
	if err := message.Validate(); err != nil {
		return SecretInstallation{}, err
	}
	if message.Type != SecretInstalled || message.Work != (messages.WorkContext{}) || !id.Valid(message.CorrelationID) || !message.ExpiresAt.IsZero() {
		return SecretInstallation{}, errors.New("secret installation requires its delivery correlation and no work or expiry context")
	}
	var installation SecretInstallation
	if err := domain.Decode(message.Payload, &installation); err != nil {
		return SecretInstallation{}, errors.New("invalid secret installation acknowledgement")
	}
	return installation, nil
}

// ValidateSecretInstalledDelivery checks the exact original delivery. Consumers
// additionally verify the active request, current secret and authorized sender.
// Historical valid acknowledgements never establish ongoing readiness.
func ValidateSecretInstalledDelivery(message, delivery messages.Message) error {
	installation, err := DecodeSecretInstalled(message)
	if err != nil {
		return err
	}
	payload, err := agentevents.DecodeSecretDelivery(delivery)
	if err != nil {
		return err
	}
	defer clear(payload.Value)
	metadata := payload.Metadata
	if message.AgentID != delivery.AgentID || message.CorrelationID != delivery.ID || installation.RequestID != delivery.CorrelationID || installation.SecretID != metadata.ID || installation.SecretVersion != metadata.Version {
		return errors.New("secret installation must acknowledge the exact recipient, request and delivered version")
	}
	if message.CreatedAt.Before(delivery.CreatedAt) || !message.CreatedAt.Before(delivery.ExpiresAt) {
		return errors.New("secret installation must occur within the delivery validity interval")
	}
	return nil
}
