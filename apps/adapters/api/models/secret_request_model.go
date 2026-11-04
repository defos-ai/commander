package models

import "time"

type SecretRequestResponse struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	AgentType   string    `json:"agent_type"`
	Name        string    `json:"name"`
	AgentID     string    `json:"agent_id"`
	OperationID string    `json:"operation_id,omitempty"`
	TaskID      string    `json:"task_id,omitempty"`
	CommandID   string    `json:"command_id,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Status      string    `json:"status"`
}

type ProvideSecretValueRequest struct {
	Value *string `json:"value"`
}

func (ProvideSecretValueRequest) String() string   { return "secret request value (redacted)" }
func (ProvideSecretValueRequest) GoString() string { return "secret request value (redacted)" }

type SecretValueSubmissionResponse struct {
	Status string `json:"status"`
}
