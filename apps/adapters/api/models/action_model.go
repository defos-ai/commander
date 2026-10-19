package models

import "encoding/json"

type ActionResponse struct {
	SessionMode         string                      `json:"session_mode,omitempty"`
	Name                string                      `json:"name"`
	Description         string                      `json:"description"`
	StatusReason        string                      `json:"status_reason,omitempty"`
	Status              string                      `json:"status"`
	MissingRequirements []ActionRequirementResponse `json:"missing_requirements,omitempty"`
	InputSchema         json.RawMessage             `json:"input_schema"`
	OutputSchema        json.RawMessage             `json:"output_schema"`
}

type ActionRequirementResponse struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Secret bool   `json:"secret,omitempty"`
}

type ActionOutputResponse struct {
	Session     *RuntimeSessionResponse `json:"session,omitempty"`
	ActionName  string                  `json:"action_name"`
	Output      json.RawMessage         `json:"output"`
	Artifacts   []ArtifactResponse      `json:"artifacts,omitempty"`
	NextActions []NextActionResponse    `json:"next_actions,omitempty"`
}
