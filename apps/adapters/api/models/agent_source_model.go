package models

import "time"

type CreateAgentSourceRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Visibility  string `json:"visibility"`
}

type SetAgentSourceTrustRequest struct {
	Version uint64 `json:"version"`
	Trusted *bool  `json:"trusted"`
}

type AgentSourceResponse struct {
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Source         string    `json:"source"`
	Visibility     string    `json:"visibility"`
	Trusted        bool      `json:"trusted"`
	Version        uint64    `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	TrustUpdatedBy string    `json:"trust_updated_by,omitempty"`
	TrustUpdatedAt time.Time `json:"trust_updated_at,omitzero"`
}
