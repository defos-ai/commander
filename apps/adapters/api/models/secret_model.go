package models

import "time"

type CreateSecretRequest struct {
	AgentType string     `json:"agent_type"`
	Name      string     `json:"name"`
	Value     *string    `json:"value"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type ReplaceSecretRequest struct {
	ID        string     `json:"id"`
	Version   uint64     `json:"version"`
	Value     *string    `json:"value"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type SecretResponse struct {
	ID        string     `json:"id"`
	AgentType string     `json:"agent_type"`
	Name      string     `json:"name"`
	Version   uint64     `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Status    string     `json:"status"`
}
