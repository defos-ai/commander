package fixtures

import (
	"encoding/json"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/id"
)

func SharingMission() models.CreateMissionRequest {
	return models.CreateMissionRequest{
		Name:         "sharing-" + id.New(),
		Objectives:   []string{"Analyze vulnerability findings as they arrive."},
		InputSchema:  json.RawMessage(`{}`),
		OutputSchema: json.RawMessage(`{}`),
		Policies: &models.MissionPoliciesRequest{
			Completion: &models.MissionCompletionPolicyRequest{DecisionBy: "operator"},
			Continuation: &models.MissionContinuationPolicyRequest{
				AggregateResults: []string{"find-vulnerabilities"},
				ModelReview:      []string{"find-vulnerabilities", "analyze-vulnerabilities"},
			},
			Placement: &models.MissionPlacementPolicyRequest{SameAgent: []string{"find-vulnerabilities", "analyze-vulnerabilities"}},
			Sharing: []models.MissionSharingPolicyRequest{{
				FromActions: []string{"find-vulnerabilities"},
				ToActions:   []string{"analyze-vulnerabilities"},
				Include:     []string{"results", "artifacts", "decisions"},
			}},
		},
	}
}

func PoliciesDefinition(definition models.CreateMissionRequest, policies any) Object {
	return Object{
		"name": definition.Name, "objectives": definition.Objectives,
		"input_schema": definition.InputSchema, "output_schema": definition.OutputSchema,
		"policies": policies,
	}
}

func InvalidSharingPolicies() [][]models.MissionSharingPolicyRequest {
	var policies [][]models.MissionSharingPolicyRequest
	for _, field := range []string{"from_actions", "to_actions", "include"} {
		policy := SharingMission().Policies.Sharing
		switch field {
		case "from_actions":
			policy[0].FromActions = nil
		case "to_actions":
			policy[0].ToActions = nil
		case "include":
			policy[0].Include = nil
		}
		policies = append(policies, policy)
	}
	for _, names := range [][]string{{"find-vulnerabilities", "find-vulnerabilities"}, {" find-vulnerabilities"}, {""}} {
		policy := SharingMission().Policies.Sharing
		policy[0].FromActions = names
		policies = append(policies, policy)
	}
	for _, kinds := range [][]string{{"results", "results"}, {"secrets"}} {
		policy := SharingMission().Policies.Sharing
		policy[0].Include = kinds
		policies = append(policies, policy)
	}
	return policies
}

func InvalidPlacementPolicies() []any {
	return []any{
		Object{"same_agent": []string{"find-vulnerabilities", "find-vulnerabilities"}},
		Object{"same_agent": []string{" find-vulnerabilities"}},
		Object{"same_agent": []string{""}},
		Object{"same_agent": "find-vulnerabilities"},
		Object{"for_actions": []string{"find-vulnerabilities"}},
		Object{"same_agent": []string{"find-vulnerabilities"}, "fallback": true},
	}
}

func InvalidContinuationPolicies() []any {
	return []any{
		Object{"aggregate_results": []string{"find-vulnerabilities", "find-vulnerabilities"}},
		Object{"aggregate_results": []string{" find-vulnerabilities"}},
		Object{"aggregate_results": []string{""}},
		Object{"aggregate_results": "find-vulnerabilities"},
		Object{"model_review": []string{"analyze-vulnerabilities", "analyze-vulnerabilities"}},
		Object{"model_review": []string{"analyze-vulnerabilities "}},
		Object{"model_review": []string{""}},
		Object{"model_review": "analyze-vulnerabilities"},
		Object{"aggregate_results": []string{"find-vulnerabilities"}, "decision_by": "model"},
		Object{"for_actions": []string{"find-vulnerabilities"}},
	}
}

func InvalidCompletionPolicies() []any {
	return []any{
		Object{},
		Object{"decision_by": ""},
		Object{"decision_by": "human"},
		Object{"decision_by": nil},
		Object{"decision_by": 1},
		Object{"decision_by": "operator", "fallback_at": "later"},
		"operator",
	}
}
