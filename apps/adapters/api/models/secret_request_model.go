package models

import "time"

type SecretRequestResponse struct {
	ID          string    `json:"id"`
	AgentType   string    `json:"agent_type"`
	Name        string    `json:"name"`
	AgentID     string    `json:"agent_id"`
	RequestedAt time.Time `json:"requested_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Status      string    `json:"status"`
}
