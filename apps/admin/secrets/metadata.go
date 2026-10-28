package secrets

import (
	"time"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

// Metadata contains no secret value or encrypted envelope. Identity and version
// are retained so a later mutation cannot replace a concurrently changed secret.
type Metadata struct {
	ID        string     `json:"id"`
	AgentType string     `json:"agent_type"`
	Name      string     `json:"name"`
	Version   uint64     `json:"version"`
	Status    string     `json:"status"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Request is pending request metadata, not proof of agent readiness or delivery.
type Request struct {
	ID          string    `json:"id"`
	AgentType   string    `json:"agent_type"`
	Name        string    `json:"name"`
	AgentID     string    `json:"agent_id"`
	RequestedAt time.Time `json:"requested_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Status      string    `json:"status"`
}

func (metadata Metadata) key() secretmodel.Key {
	return secretmodel.Key{Scope: secretmodel.Scope{AgentType: metadata.AgentType}, Name: metadata.Name}
}

func validMetadata(metadata Metadata, key secretmodel.Key) bool {
	if !id.Valid(metadata.ID) || metadata.key() != key || key.Validate() != nil || metadata.Version == 0 ||
		(metadata.Status != "available" && metadata.Status != "expired") {
		return false
	}
	for _, value := range []*time.Time{metadata.CreatedAt, metadata.UpdatedAt, metadata.ExpiresAt} {
		if value != nil && !validTime(*value) {
			return false
		}
	}
	return metadata.CreatedAt == nil || metadata.UpdatedAt == nil || !metadata.UpdatedAt.Before(*metadata.CreatedAt)
}

func validRequest(request Request) bool {
	key := secretmodel.Key{Scope: secretmodel.Scope{AgentType: request.AgentType}, Name: request.Name}
	if !id.Valid(request.ID) || key.Validate() != nil || !id.ValidAgent(request.AgentID) ||
		!validTime(request.RequestedAt) || !validTime(request.ExpiresAt) || !request.ExpiresAt.After(request.RequestedAt) {
		return false
	}
	return request.Status == "needs_value" || request.Status == "awaiting_delivery" || request.Status == "awaiting_installation"
}

func validTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}
