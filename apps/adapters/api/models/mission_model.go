package models

import "encoding/json"

type MissionResponse struct {
	Automations     map[string]MissionAutomationResponse `json:"automations,omitempty"`
	Name            string                               `json:"name"`
	Objective       string                               `json:"objective"`
	RequiredActions []string                             `json:"required_actions"`
	StartingActions []string                             `json:"starting_actions"`
	Steps           []MissionStepResponse                `json:"steps,omitempty"`
	InputSchema     json.RawMessage                      `json:"input_schema"`
	OutputSchema    json.RawMessage                      `json:"output_schema"`
}

type MissionStepResponse struct {
	ID              string   `json:"id"`
	Objective       string   `json:"objective"`
	AgentTypes      []string `json:"agent_types"`
	RequiredActions []string `json:"required_actions"`
}

type MissionAutomationResponse struct {
	Run      []string `json:"run,omitempty"`
	Continue string   `json:"continue,omitempty"`
	Decide   string   `json:"decide,omitempty"`
}
