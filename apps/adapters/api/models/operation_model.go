package models

import (
	"encoding/json"
	"time"
)

type StartOperationRequest struct {
	OperationID string          `json:"operation_id,omitempty"`
	MissionName string          `json:"mission_name"`
	Input       json.RawMessage `json:"input"`
}

type CancelOperationRequest struct{}

type CompleteOperationRequest struct {
	Output json.RawMessage `json:"output"`
}

type FailOperationRequest struct {
	Reason string `json:"reason"`
}

type OperationResponse struct {
	ID        string             `json:"id"`
	Mission   MissionResponse    `json:"mission"`
	Input     json.RawMessage    `json:"input"`
	Status    string             `json:"status"`
	Preflight *PreflightResponse `json:"preflight,omitempty"`
	Tasks     []TaskResponse     `json:"tasks"`
	Questions []QuestionResponse `json:"questions"`
	Output    json.RawMessage    `json:"output,omitempty"`
	ReportID  string             `json:"report_id,omitempty"`
	Error     string             `json:"error,omitempty"`
	Events    []EventResponse    `json:"events"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}
