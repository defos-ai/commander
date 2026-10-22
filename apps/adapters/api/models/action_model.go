package models

import "encoding/json"

type ActionDefinitionRequest struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	RequiredSecrets []string        `json:"required_secrets,omitempty"`
	InputSchema     json.RawMessage `json:"input_schema"`
	OutputSchema    json.RawMessage `json:"output_schema"`
}

type ActionDefinitionResponse struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	RequiredSecrets []string        `json:"required_secrets,omitempty"`
	InputSchema     json.RawMessage `json:"input_schema"`
	OutputSchema    json.RawMessage `json:"output_schema"`
}

type ActionResponse struct {
	Name                string                      `json:"name"`
	Description         string                      `json:"description"`
	StatusReason        string                      `json:"status_reason,omitempty"`
	Status              string                      `json:"status"`
	MissingRequirements []ActionRequirementResponse `json:"missing_requirements,omitempty"`
	RequiredSecrets     []string                    `json:"required_secrets,omitempty"`
	InputSchema         json.RawMessage             `json:"input_schema"`
	OutputSchema        json.RawMessage             `json:"output_schema"`
}

type ActionRequirementResponse struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type ActionOutputResponse struct {
	ActionName  string               `json:"action_name"`
	Output      json.RawMessage      `json:"output"`
	Artifacts   []ArtifactResponse   `json:"artifacts,omitempty"`
	NextActions []NextActionResponse `json:"next_actions,omitempty"`
}
