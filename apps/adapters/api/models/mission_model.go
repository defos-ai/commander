package models

import "encoding/json"

type CreateMissionRequest struct {
	Rules             map[string]MissionRuleRequest `json:"rules,omitempty"`
	Name              string                        `json:"name"`
	Objectives        []string                      `json:"objectives"`
	ActionDefinitions []ActionDefinitionRequest     `json:"action_definitions,omitempty"`
	RequiredActions   []string                      `json:"required_actions,omitempty"`
	OptionalActions   []string                      `json:"optional_actions,omitempty"`
	StartingActions   []string                      `json:"starting_actions,omitempty"`
	InputSchema       json.RawMessage               `json:"input_schema"`
	OutputSchema      json.RawMessage               `json:"output_schema"`
}

type MissionRuleRequest struct {
	ForActions []string                  `json:"for_actions"`
	Params     *MissionRuleParamsRequest `json:"params,omitempty"`
}

type MissionRuleParamsRequest struct {
	DecisionBy string `json:"decision_by,omitempty"`
}

type MissionResponse struct {
	Rules             map[string]MissionRuleResponse `json:"rules,omitempty"`
	Name              string                         `json:"name"`
	Objectives        []string                       `json:"objectives"`
	ActionDefinitions []ActionDefinitionResponse     `json:"action_definitions,omitempty"`
	RequiredActions   []string                       `json:"required_actions"`
	OptionalActions   []string                       `json:"optional_actions,omitempty"`
	StartingActions   []string                       `json:"starting_actions"`
	InputSchema       json.RawMessage                `json:"input_schema"`
	OutputSchema      json.RawMessage                `json:"output_schema"`
}

type MissionRuleResponse struct {
	ForActions []string                   `json:"for_actions"`
	Params     *MissionRuleParamsResponse `json:"params,omitempty"`
}

type MissionRuleParamsResponse struct {
	DecisionBy string `json:"decision_by,omitempty"`
}
