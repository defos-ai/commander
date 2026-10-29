package runner_test

import (
	"encoding/json"
	"testing"

	"github.com/defos-ai/commander/framework/domain/missions"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestMissionCompletionPolicyValidation(t *testing.T) {
	mission := fixtures.Config().MissionStore.(*fakes.MissionStore).Snapshot()
	definition, err := json.Marshal(mission)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, policy string
		decision     missions.Decision
		invalid      bool
	}{
		{name: "omitted"},
		{name: "null", policy: "null"},
		{name: "model", policy: `{"decision_by":"model"}`, decision: missions.Model},
		{name: "operator", policy: `{"decision_by":"operator"}`, decision: missions.Operator},
		{name: "empty policy", policy: `{}`, invalid: true},
		{name: "empty decision", policy: `{"decision_by":""}`, invalid: true},
		{name: "null decision", policy: `{"decision_by":null}`, invalid: true},
		{name: "unsupported decision", policy: `{"decision_by":"code"}`, invalid: true},
		{name: "case-sensitive decision", policy: `{"decision_by":"Operator"}`, invalid: true},
		{name: "untrimmed decision", policy: `{"decision_by":"operator "}`, invalid: true},
		{name: "action-scoped policy", policy: `{"decision_by":"operator","for_actions":["inspect"]}`, invalid: true},
		{name: "string policy", policy: `"operator"`, invalid: true},
		{name: "array policy", policy: `[]`, invalid: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(definition, &fields); err != nil {
				t.Fatal(err)
			}
			if scenario.policy != "" {
				fields["completion"] = json.RawMessage(scenario.policy)
			}
			input, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}

			loaded, err := missions.LoadMission(input)

			if scenario.invalid {
				if err == nil {
					t.Fatal("invalid completion policy was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario.decision != "" {
				if loaded.Completion == nil || loaded.Completion.DecisionBy != scenario.decision {
					t.Fatal("completion policy changed during decoding")
				}
				return
			}
			if loaded.Completion != nil {
				t.Fatal("an omitted completion policy became explicit")
			}
			roundTrip, err := json.Marshal(loaded)
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]json.RawMessage
			if json.Unmarshal(roundTrip, &saved) != nil {
				t.Fatal("mission did not encode as an object")
			}
			if _, exists := saved["completion"]; exists {
				t.Fatal("an absent completion policy was written to JSON")
			}
		})
	}
}
