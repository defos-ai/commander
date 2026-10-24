package models

import (
	"encoding/json"
	"time"
)

type AssignTaskRequest struct {
	StepID     string          `json:"step_id,omitempty"`
	ActionName string          `json:"action_name"`
	Input      json.RawMessage `json:"input,omitempty"`
}

type SessionIntentResponse struct {
	Mode    string `json:"mode"`
	ScopeID string `json:"scope_id"`
	ID      string `json:"id,omitempty"`
	Error   string `json:"error,omitempty"`
}
type TaskResponse struct {
	SessionIntent       *SessionIntentResponse      `json:"session_intent,omitempty"`
	ID                  string                      `json:"id"`
	OperationID         string                      `json:"operation_id"`
	ParentTaskID        string                      `json:"parent_task_id,omitempty"`
	StepID              string                      `json:"step_id,omitempty"`
	AgentID             string                      `json:"agent_id"`
	AgentType           string                      `json:"agent_type,omitempty"`
	MissingRequirements []ActionRequirementResponse `json:"missing_requirements,omitempty"`
	ActionName          string                      `json:"action_name"`
	CommandID           string                      `json:"command_id,omitempty"`
	DeadlineAt          *time.Time                  `json:"deadline_at,omitempty"`
	Input               json.RawMessage             `json:"input,omitempty"`
	Status              string                      `json:"status"`
	Result              *ActionOutputResponse       `json:"result,omitempty"`
	Continuation        *ContinuationResponse       `json:"continuation,omitempty"`
	Error               string                      `json:"error,omitempty"`
	StartedAt           time.Time                   `json:"started_at"`
	FinishedAt          *time.Time                  `json:"finished_at,omitempty"`
}
