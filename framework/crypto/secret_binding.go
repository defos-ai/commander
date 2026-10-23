package crypto

import (
	"encoding/json"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

// bindingV2 is the fixed authenticated encoding for Format. Do not serialize the
// domain struct directly: adding a domain field must not silently change this
// format. Changes to authenticated fields require a new envelope format.
type bindingV2 struct {
	Format    string `json:"format"`
	KeyID     string `json:"key_id"`
	ID        string `json:"id"`
	AgentType string `json:"agent_type"`
	Name      string `json:"name"`
	Version   uint64 `json:"version"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	ExpiresAt string `json:"expires_at"`
}

func authenticatedData(metadata secrets.Metadata, format, keyID string) []byte {
	value := bindingV2{
		Format: format, KeyID: keyID, ID: metadata.ID,
		AgentType: metadata.Key.Scope.AgentType,
		Name:      metadata.Key.Name, Version: metadata.Version,
		CreatedAt: timestamp(metadata.CreatedAt), UpdatedAt: timestamp(metadata.UpdatedAt),
		ExpiresAt: timestamp(metadata.ExpiresAt),
	}
	// Only strings and uint64 are encoded, so Marshal cannot fail.
	data, _ := json.Marshal(value)
	return data
}

func timestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	// Preserve nanoseconds and support the full domain year range. Providers must
	// not truncate these timestamps (for example through BSON's millisecond dates).
	return value.UTC().Format(time.RFC3339Nano)
}
