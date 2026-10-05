package models

import "encoding/json"

type ResolveContinuationRequest struct {
	Version uint64              `json:"version"`
	Actions []NextActionRequest `json:"actions"`
}

type NextActionRequest struct {
	ActionName string          `json:"action_name"`
	Input      json.RawMessage `json:"input,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

type ContinuationResponse struct {
	Version         uint64               `json:"version"`
	DecisionBy      string               `json:"decision_by"`
	Status          string               `json:"status"`
	ProposedActions []NextActionResponse `json:"proposed_actions"`
	CreatedTaskIDs  []string             `json:"created_task_ids"`
	QuestionID      string               `json:"question_id,omitempty"`
}

type NextActionResponse struct {
	ActionName string          `json:"action_name"`
	Input      json.RawMessage `json:"input,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}
