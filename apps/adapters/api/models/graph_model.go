package models

import "time"

type OperationGraphResponse struct {
	OperationID string              `json:"operation_id"`
	Status      string              `json:"status"`
	UpdatedAt   time.Time           `json:"updated_at"`
	Nodes       []GraphNodeResponse `json:"nodes"`
	Edges       []GraphEdgeResponse `json:"edges"`
}

type GraphNodeResponse struct {
	ID       string                 `json:"id"`
	Kind     string                 `json:"kind"`
	Task     *GraphTaskResponse     `json:"task,omitempty"`
	Artifact *GraphArtifactResponse `json:"artifact,omitempty"`
	File     *GraphFileResponse     `json:"file,omitempty"`
}

type GraphEdgeResponse struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type GraphTaskResponse struct {
	ID           string     `json:"id"`
	ParentTaskID string     `json:"parent_task_id,omitempty"`
	ActionName   string     `json:"action_name"`
	AgentID      string     `json:"agent_id,omitempty"`
	CommandID    string     `json:"command_id,omitempty"`
	Status       string     `json:"status"`
	Error        string     `json:"error,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

type GraphArtifactResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	AgentID    string    `json:"agent_id"`
	ActionName string    `json:"action_name"`
	CommandID  string    `json:"command_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type GraphFileResponse struct {
	ArtifactID string `json:"artifact_id"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	MediaType  string `json:"media_type"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}
