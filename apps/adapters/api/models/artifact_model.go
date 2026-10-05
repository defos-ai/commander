package models

import "time"

type ArtifactResponse struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	AgentID    string                 `json:"agent_id"`
	ActionName string                 `json:"action_name"`
	CommandID  string                 `json:"command_id,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	Files      []ArtifactFileResponse `json:"files"`
}

type ArtifactFileResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}
