package models

import "encoding/json"

type CreateMissionRequest struct {
	Context           *MissionContextPolicyRequest    `json:"context,omitempty"`
	Completion        *MissionCompletionPolicyRequest `json:"completion,omitempty"`
	Rules             map[string]MissionRuleRequest   `json:"rules,omitempty"`
	Name              string                          `json:"name"`
	Objectives        []string                        `json:"objectives"`
	ActionDefinitions []ActionDefinitionRequest       `json:"action_definitions,omitempty"`
	RequiredActions   []string                        `json:"required_actions,omitempty"`
	OptionalActions   []string                        `json:"optional_actions,omitempty"`
	StartingActions   []string                        `json:"starting_actions,omitempty"`
	InputSchema       json.RawMessage                 `json:"input_schema"`
	OutputSchema      json.RawMessage                 `json:"output_schema"`
}

type MissionContextPolicyRequest struct {
	Sharing []MissionContextSharingRequest `json:"sharing,omitempty"`
}

type MissionContextSharingRequest struct {
	FromActions []string `json:"from_actions"`
	ToActions   []string `json:"to_actions"`
	Include     []string `json:"include"`
}

type MissionCompletionPolicyRequest struct {
	DecisionBy string `json:"decision_by"`
}

type MissionRuleRequest struct {
	ForActions []string                  `json:"for_actions"`
	Params     *MissionRuleParamsRequest `json:"params,omitempty"`
}

type MissionRuleParamsRequest struct {
	DecisionBy string `json:"decision_by,omitempty"`
}

type MissionResponse struct {
	Context           *MissionContextPolicyResponse    `json:"context,omitempty"`
	Completion        *MissionCompletionPolicyResponse `json:"completion,omitempty"`
	Rules             map[string]MissionRuleResponse   `json:"rules,omitempty"`
	Name              string                           `json:"name"`
	Objectives        []string                         `json:"objectives"`
	ActionDefinitions []ActionDefinitionResponse       `json:"action_definitions,omitempty"`
	RequiredActions   []string                         `json:"required_actions"`
	OptionalActions   []string                         `json:"optional_actions,omitempty"`
	StartingActions   []string                         `json:"starting_actions"`
	InputSchema       json.RawMessage                  `json:"input_schema"`
	OutputSchema      json.RawMessage                  `json:"output_schema"`
}

type MissionContextPolicyResponse struct {
	Sharing []MissionContextSharingResponse `json:"sharing,omitempty"`
}

type MissionContextSharingResponse struct {
	FromActions []string `json:"from_actions"`
	ToActions   []string `json:"to_actions"`
	Include     []string `json:"include"`
}

type MissionCompletionPolicyResponse struct {
	DecisionBy string `json:"decision_by"`
}

type MissionRuleResponse struct {
	ForActions []string                   `json:"for_actions"`
	Params     *MissionRuleParamsResponse `json:"params,omitempty"`
}

type MissionRuleParamsResponse struct {
	DecisionBy string `json:"decision_by,omitempty"`
}
