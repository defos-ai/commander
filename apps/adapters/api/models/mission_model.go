package models

import "encoding/json"

type CreateMissionRequest struct {
	Policies          *MissionPoliciesRequest   `json:"policies,omitempty"`
	Name              string                    `json:"name"`
	Objectives        []string                  `json:"objectives"`
	ActionDefinitions []ActionDefinitionRequest `json:"action_definitions,omitempty"`
	RequiredActions   []string                  `json:"required_actions,omitempty"`
	OptionalActions   []string                  `json:"optional_actions,omitempty"`
	StartingActions   []string                  `json:"starting_actions,omitempty"`
	InputSchema       json.RawMessage           `json:"input_schema"`
	OutputSchema      json.RawMessage           `json:"output_schema"`
}

type MissionPoliciesRequest struct {
	Completion   *MissionCompletionPolicyRequest   `json:"completion,omitempty"`
	Continuation *MissionContinuationPolicyRequest `json:"continuation,omitempty"`
	Sharing      []MissionSharingPolicyRequest     `json:"sharing,omitempty"`
}

type MissionContinuationPolicyRequest struct {
	AggregateResults []string `json:"aggregate_results,omitempty"`
	ModelReview      []string `json:"model_review,omitempty"`
}

type MissionSharingPolicyRequest struct {
	FromActions []string `json:"from_actions"`
	ToActions   []string `json:"to_actions"`
	Include     []string `json:"include"`
}

type MissionCompletionPolicyRequest struct {
	DecisionBy string `json:"decision_by"`
}

type MissionResponse struct {
	Policies          *MissionPoliciesResponse   `json:"policies,omitempty"`
	Name              string                     `json:"name"`
	Objectives        []string                   `json:"objectives"`
	ActionDefinitions []ActionDefinitionResponse `json:"action_definitions,omitempty"`
	RequiredActions   []string                   `json:"required_actions"`
	OptionalActions   []string                   `json:"optional_actions,omitempty"`
	StartingActions   []string                   `json:"starting_actions"`
	InputSchema       json.RawMessage            `json:"input_schema"`
	OutputSchema      json.RawMessage            `json:"output_schema"`
}

type MissionPoliciesResponse struct {
	Completion   *MissionCompletionPolicyResponse   `json:"completion,omitempty"`
	Continuation *MissionContinuationPolicyResponse `json:"continuation,omitempty"`
	Sharing      []MissionSharingPolicyResponse     `json:"sharing,omitempty"`
}

type MissionContinuationPolicyResponse struct {
	AggregateResults []string `json:"aggregate_results,omitempty"`
	ModelReview      []string `json:"model_review,omitempty"`
}

type MissionSharingPolicyResponse struct {
	FromActions []string `json:"from_actions"`
	ToActions   []string `json:"to_actions"`
	Include     []string `json:"include"`
}

type MissionCompletionPolicyResponse struct {
	DecisionBy string `json:"decision_by"`
}
