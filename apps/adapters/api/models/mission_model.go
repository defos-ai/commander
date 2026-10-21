package models

import "encoding/json"

type CreateMissionRequest struct {
	Rules           map[string]MissionRuleRequest `json:"rules,omitempty"`
	Name            string                        `json:"name"`
	Objectives      []string                      `json:"objectives"`
	RequiredActions []string                      `json:"required_actions,omitempty"`
	OptionalActions []string                      `json:"optional_actions,omitempty"`
	StartingActions []string                      `json:"starting_actions,omitempty"`
	Steps           []MissionStepRequest          `json:"steps,omitempty"`
	InputSchema     json.RawMessage               `json:"input_schema"`
	OutputSchema    json.RawMessage               `json:"output_schema"`
}

type MissionStepRequest struct {
	ID              string   `json:"id"`
	Objective       string   `json:"objective"`
	AgentTypes      []string `json:"agent_types,omitempty"`
	RequiredActions []string `json:"required_actions,omitempty"`
}

type MissionRuleRequest struct {
	ForActions []string                  `json:"for_actions"`
	Params     *MissionRuleParamsRequest `json:"params,omitempty"`
}

type MissionRuleParamsRequest struct {
	DecisionBy string `json:"decision_by,omitempty"`
}

type MissionResponse struct {
	Rules           map[string]MissionRuleResponse `json:"rules,omitempty"`
	Name            string                         `json:"name"`
	Objectives      []string                       `json:"objectives"`
	RequiredActions []string                       `json:"required_actions"`
	OptionalActions []string                       `json:"optional_actions,omitempty"`
	StartingActions []string                       `json:"starting_actions"`
	Steps           []MissionStepResponse          `json:"steps,omitempty"`
	InputSchema     json.RawMessage                `json:"input_schema"`
	OutputSchema    json.RawMessage                `json:"output_schema"`
}

type MissionStepResponse struct {
	ID              string   `json:"id"`
	Objective       string   `json:"objective"`
	AgentTypes      []string `json:"agent_types"`
	RequiredActions []string `json:"required_actions"`
}

type MissionRuleResponse struct {
	ForActions []string                   `json:"for_actions"`
	Params     *MissionRuleParamsResponse `json:"params,omitempty"`
}

type MissionRuleParamsResponse struct {
	DecisionBy string `json:"decision_by,omitempty"`
}
