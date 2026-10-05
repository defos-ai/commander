package models

import "encoding/json"

type AssignTaskRequest struct {
	StepID     string          `json:"step_id,omitempty"`
	ActionName string          `json:"action_name"`
	Input      json.RawMessage `json:"input,omitempty"`
}
