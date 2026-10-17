package secrets

import (
	"bytes"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

// Delivery reserves the exact encrypted snapshot and envelope identity to publish.
// Its existence does not mean publication or installation succeeded. Retries must
// reuse these fields; a changed secret version requires a new request.
type Delivery struct {
	ID        string    `json:"id"`
	RequestID string    `json:"request_id"`
	AgentID   string    `json:"agent_id"`
	Secret    Secret    `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (delivery Delivery) Validate() error {
	if !id.Valid(delivery.ID) || !id.Valid(delivery.RequestID) || !id.ValidAgent(delivery.AgentID) {
		return errors.New("secret delivery requires valid delivery, request and recipient identities")
	}
	if err := delivery.Secret.Validate(); err != nil {
		return err
	}
	if !validTime(delivery.CreatedAt) || !validTime(delivery.ExpiresAt) || !delivery.ExpiresAt.After(delivery.CreatedAt) {
		return errors.New("secret delivery requires valid timestamps and a deadline after creation")
	}
	metadata := delivery.Secret.Metadata
	if metadata.UpdatedAt.After(delivery.CreatedAt) || AvailabilityAt(&metadata, delivery.CreatedAt) != Available ||
		(!metadata.ExpiresAt.IsZero() && delivery.ExpiresAt.After(metadata.ExpiresAt)) {
		return errors.New("secret delivery must fit the secret validity interval")
	}
	return nil
}

func (delivery Delivery) Clone() Delivery {
	delivery.Secret = delivery.Secret.Clone()
	return delivery
}

func (delivery Delivery) matchesRequest(request Request) bool {
	deadline := request.ExpiresAt
	if expires := delivery.Secret.Metadata.ExpiresAt; !expires.IsZero() && expires.Before(deadline) {
		deadline = expires
	}
	return delivery.RequestID == request.ID && delivery.AgentID == request.AgentID &&
		delivery.Secret.Metadata.Key == request.Key && !delivery.CreatedAt.Before(request.RequestedAt) &&
		delivery.CreatedAt.Before(request.ExpiresAt) && delivery.ExpiresAt.Equal(deadline)
}

func (delivery Delivery) equal(other Delivery) bool {
	a, b := delivery.Secret, other.Secret
	return delivery.ID == other.ID && delivery.RequestID == other.RequestID && delivery.AgentID == other.AgentID &&
		delivery.CreatedAt.Equal(other.CreatedAt) && delivery.ExpiresAt.Equal(other.ExpiresAt) &&
		a.Metadata.ID == b.Metadata.ID && a.Metadata.Key == b.Metadata.Key && a.Metadata.Version == b.Metadata.Version &&
		a.Metadata.CreatedAt.Equal(b.Metadata.CreatedAt) && a.Metadata.UpdatedAt.Equal(b.Metadata.UpdatedAt) &&
		a.Metadata.ExpiresAt.Equal(b.Metadata.ExpiresAt) && a.Value.Format == b.Value.Format && a.Value.KeyID == b.Value.KeyID &&
		bytes.Equal(a.Value.Nonce, b.Value.Nonce) && bytes.Equal(a.Value.Ciphertext, b.Value.Ciphertext)
}

// Installation is an agent's acknowledgement, not a broker receipt. Hosts must
// authenticate its source before saving it. It records history, not readiness.
type Installation struct {
	ID            string    `json:"id"`
	RequestID     string    `json:"request_id"`
	DeliveryID    string    `json:"delivery_id"`
	AgentID       string    `json:"agent_id"`
	SecretID      string    `json:"secret_id"`
	SecretVersion uint64    `json:"secret_version"`
	InstalledAt   time.Time `json:"installed_at"`
}

func (installation Installation) Validate() error {
	if !id.Valid(installation.ID) || !id.Valid(installation.RequestID) || !id.Valid(installation.DeliveryID) ||
		!id.ValidAgent(installation.AgentID) || !id.Valid(installation.SecretID) || installation.SecretVersion == 0 ||
		!validTime(installation.InstalledAt) {
		return errors.New("secret installation requires valid identities, version and timestamp")
	}
	return nil
}

func (installation Installation) matchesDelivery(delivery Delivery) bool {
	return installation.RequestID == delivery.RequestID && installation.DeliveryID == delivery.ID &&
		installation.AgentID == delivery.AgentID && installation.SecretID == delivery.Secret.Metadata.ID &&
		installation.SecretVersion == delivery.Secret.Metadata.Version &&
		!installation.InstalledAt.Before(delivery.CreatedAt) && installation.InstalledAt.Before(delivery.ExpiresAt)
}

func (installation Installation) equal(other Installation) bool {
	return installation.ID == other.ID && installation.RequestID == other.RequestID &&
		installation.DeliveryID == other.DeliveryID && installation.AgentID == other.AgentID &&
		installation.SecretID == other.SecretID && installation.SecretVersion == other.SecretVersion &&
		installation.InstalledAt.Equal(other.InstalledAt)
}
