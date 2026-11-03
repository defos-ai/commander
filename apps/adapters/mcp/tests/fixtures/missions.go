package fixtures

import (
	"encoding/json"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/id"
)

func MissionDefinition(mission missions.Mission) Object {
	return Object{
		"name": mission.Name, "objectives": mission.Objectives,
		"required_actions": mission.RequiredActions, "optional_actions": mission.OptionalActions,
		"starting_actions": mission.StartingActions, "action_definitions": mission.ActionDefinitions,
		"policies":     mission.Policies,
		"input_schema": mission.InputSchema, "output_schema": mission.OutputSchema,
	}
}

func (a *Client) CancelActiveOperation(id string) {
	a.t.Helper()
	if operation := a.Get(id); !operation.Status.Terminal() {
		a.Call("cancel_operation", Object{"operation_id": id}, nil)
	}
}

func SharingMission() missions.Mission {
	return missions.Mission{
		Name:         "sharing-" + id.New(),
		Objectives:   []string{"Analyze vulnerability findings as they arrive."},
		InputSchema:  json.RawMessage(`{}`),
		OutputSchema: json.RawMessage(`{}`),
		Policies: &policies.MissionPolicies{
			Completion: &policies.CompletionPolicy{DecisionBy: policies.Operator},
			Session:    []policies.SessionPolicy{{CreateAction: "open-assessment", UseActions: []string{"analyze-vulnerabilities"}}},
			Continuation: &policies.ContinuationPolicy{
				AggregateResults: []string{"find-vulnerabilities"},
				ModelReview:      []string{"find-vulnerabilities", "analyze-vulnerabilities"},
			},
			Sharing: policies.SharingPolicy{{
				FromActions: []string{"find-vulnerabilities"},
				ToActions:   []string{"analyze-vulnerabilities"},
				Include:     []policies.SharingKind{policies.ShareResults, policies.ShareArtifacts},
			}},
		},
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

func InvalidSessionPolicies() []any {
	return []any{
		Object{"create_action": "open-assessment", "use_actions": []string{"analyze-vulnerabilities"}},
		[]any{nil},
		[]Object{{"use_actions": []string{"analyze-vulnerabilities"}}},
		[]Object{{"create_action": "open-assessment", "use_actions": []string{}}},
		[]Object{{"create_action": "open-assessment", "use_actions": nil}},
		[]Object{{"create_action": " open-assessment", "use_actions": []string{"analyze-vulnerabilities"}}},
		[]Object{{"create_action": "open-assessment", "use_actions": []string{"analyze-vulnerabilities "}}},
		[]Object{{"create_action": "open-assessment", "use_actions": []string{"analyze-vulnerabilities", "analyze-vulnerabilities"}}},
		[]Object{{"create_action": "open-assessment", "use_actions": []string{"open-assessment"}}},
		[]Object{{"create_action": "open-assessment", "use_actions": []string{"analyze-vulnerabilities"}, "scope": "operation"}},
		[]Object{
			{"create_action": "open-assessment", "use_actions": []string{"analyze-vulnerabilities"}},
			{"create_action": "open-assessment", "use_actions": []string{"review-vulnerabilities"}},
		},
		[]Object{
			{"create_action": "open-assessment", "use_actions": []string{"analyze-vulnerabilities"}},
			{"create_action": "analyze-vulnerabilities", "use_actions": []string{"review-vulnerabilities"}},
		},
	}
}
