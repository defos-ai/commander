package models

import "time"

type CreateCatalogAgentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Package     string `json:"package"`
	Visibility  string `json:"visibility"`
}

type SetCatalogAgentTrustRequest struct {
	Version uint64 `json:"version"`
	Trusted *bool  `json:"trusted"`
}

type CatalogAgentResponse struct {
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Package        string    `json:"package"`
	Visibility     string    `json:"visibility"`
	Trusted        bool      `json:"trusted"`
	Version        uint64    `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	TrustUpdatedBy string    `json:"trust_updated_by,omitempty"`
	TrustUpdatedAt time.Time `json:"trust_updated_at,omitzero"`
}
