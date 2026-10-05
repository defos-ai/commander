package models

import "encoding/json"

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
