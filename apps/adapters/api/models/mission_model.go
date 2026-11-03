package models

import "encoding/json"

type CreateMissionRequest struct {
	Policies          *MissionPoliciesRequest         `json:"policies,omitempty"`
	Completion        *MissionCompletionPolicyRequest `json:"completion,omitempty"`
	Name              string                          `json:"name"`
	Objectives        []string                        `json:"objectives"`
	ActionDefinitions []ActionDefinitionRequest       `json:"action_definitions,omitempty"`
	RequiredActions   []string                        `json:"required_actions,omitempty"`
	OptionalActions   []string                        `json:"optional_actions,omitempty"`
	StartingActions   []string                        `json:"starting_actions,omitempty"`
	InputSchema       json.RawMessage                 `json:"input_schema"`
	OutputSchema      json.RawMessage                 `json:"output_schema"`
}

type MissionPoliciesRequest struct {
	Continuation *MissionContinuationPolicyRequest `json:"continuation,omitempty"`
	Placement    *MissionPlacementPolicyRequest    `json:"placement,omitempty"`
	Sharing      []MissionSharingPolicyRequest     `json:"sharing,omitempty"`
}

type MissionContinuationPolicyRequest struct {
	AggregateResults []string `json:"aggregate_results,omitempty"`
	ModelReview      []string `json:"model_review,omitempty"`
}

type MissionPlacementPolicyRequest struct {
	SameAgent []string `json:"same_agent,omitempty"`
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
	Policies          *MissionPoliciesResponse         `json:"policies,omitempty"`
	Completion        *MissionCompletionPolicyResponse `json:"completion,omitempty"`
	Name              string                           `json:"name"`
	Objectives        []string                         `json:"objectives"`
	ActionDefinitions []ActionDefinitionResponse       `json:"action_definitions,omitempty"`
	RequiredActions   []string                         `json:"required_actions"`
	OptionalActions   []string                         `json:"optional_actions,omitempty"`
	StartingActions   []string                         `json:"starting_actions"`
	InputSchema       json.RawMessage                  `json:"input_schema"`
	OutputSchema      json.RawMessage                  `json:"output_schema"`
}

type MissionPoliciesResponse struct {
	Continuation *MissionContinuationPolicyResponse `json:"continuation,omitempty"`
	Placement    *MissionPlacementPolicyResponse    `json:"placement,omitempty"`
	Sharing      []MissionSharingPolicyResponse     `json:"sharing,omitempty"`
}

type MissionContinuationPolicyResponse struct {
	AggregateResults []string `json:"aggregate_results,omitempty"`
	ModelReview      []string `json:"model_review,omitempty"`
}

type MissionPlacementPolicyResponse struct {
	SameAgent []string `json:"same_agent,omitempty"`
}

type MissionSharingPolicyResponse struct {
	FromActions []string `json:"from_actions"`
	ToActions   []string `json:"to_actions"`
	Include     []string `json:"include"`
}

type MissionCompletionPolicyResponse struct {
	DecisionBy string `json:"decision_by"`
}
