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
