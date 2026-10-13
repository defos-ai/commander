package runner_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/domain/missions"
)

func TestMissionRulesValidation(t *testing.T) {
	tests := []struct {
		name  string
		rules missions.RuleSet
		want  string
	}{
		{name: "no rules"},
		{name: "empty rules", rules: missions.RuleSet{}},
		{name: "aggregate defaults", rules: missions.RuleSet{
			missions.AggregateResults: {Actions: []string{"batch-ports"}},
		}},
		{name: "aggregate empty parameters", rules: missions.RuleSet{
			missions.AggregateResults: {Actions: []string{"batch-ports"}, Params: &missions.RuleParams{}},
		}},
		{name: "composable rules on same action", rules: missions.RuleSet{
			missions.AggregateResults:  {Actions: []string{"batch-ports"}},
			missions.DecideNextActions: {Actions: []string{"batch-ports", "scan-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		}},
		{name: "unknown rule", rules: missions.RuleSet{
			"unknown": {Actions: []string{"scan-ports"}},
		}, want: `rule "unknown": unsupported rule`},
		{name: "empty selectors", rules: missions.RuleSet{
			missions.AggregateResults: {},
		}, want: "actions must not be empty"},
		{name: "blank selector", rules: missions.RuleSet{
			missions.AggregateResults: {Actions: []string{"batch-ports", " \t"}},
		}, want: "nonblank names"},
		{name: "leading whitespace", rules: missions.RuleSet{
			missions.AggregateResults: {Actions: []string{" batch-ports"}},
		}, want: "without surrounding whitespace"},
		{name: "trailing whitespace", rules: missions.RuleSet{
			missions.AggregateResults: {Actions: []string{"batch-ports\n"}},
		}, want: "without surrounding whitespace"},
		{name: "duplicate selector", rules: missions.RuleSet{
			missions.AggregateResults: {Actions: []string{"batch-ports", "batch-ports"}},
		}, want: `duplicate action "batch-ports"`},
		{name: "aggregation decision parameters", rules: missions.RuleSet{
			missions.AggregateResults: {Actions: []string{"batch-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		}, want: "does not accept decision_by"},
		{name: "missing decision parameters", rules: missions.RuleSet{
			missions.DecideNextActions: {Actions: []string{"scan-ports"}},
		}, want: `decision_by must be "model"`},
		{name: "empty decision", rules: missions.RuleSet{
			missions.DecideNextActions: {Actions: []string{"scan-ports"}, Params: &missions.RuleParams{}},
		}, want: `decision_by must be "model"`},
		{name: "unsupported decision", rules: missions.RuleSet{
			missions.DecideNextActions: {Actions: []string{"scan-ports"}, Params: &missions.RuleParams{DecisionBy: "other"}},
		}, want: `decision_by must be "model"`},
		{name: "stable first error", rules: missions.RuleSet{
			missions.DecideNextActions: {},
			missions.AggregateResults:  {},
		}, want: `rule "aggregate-results": actions must not be empty`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rules.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestMissionRulesCloneIsolatesConfiguration(t *testing.T) {
	original := missions.RuleSet{
		missions.AggregateResults:  {Actions: []string{"batch-ports"}},
		missions.DecideNextActions: {Actions: []string{"scan-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
	cloned := original.Clone()
	if !reflect.DeepEqual(original, cloned) {
		t.Fatalf("clone changed configuration: %+v", cloned)
	}
	cloned[missions.AggregateResults].Actions[0] = "other-batch"
	cloned[missions.DecideNextActions].Actions[0] = "other-scan"
	cloned[missions.DecideNextActions].Params.DecisionBy = "other"
	delete(cloned, missions.AggregateResults)
	if original[missions.AggregateResults].Actions[0] != "batch-ports" ||
		original[missions.DecideNextActions].Actions[0] != "scan-ports" ||
		original[missions.DecideNextActions].Params.DecisionBy != missions.Model {
		t.Fatalf("editing clone changed original: %+v", original)
	}
	for _, rules := range []missions.RuleSet{nil, {}, {missions.AggregateResults: {}}, {missions.AggregateResults: {Actions: []string{}}}} {
		if !reflect.DeepEqual(rules, rules.Clone()) {
			t.Fatalf("clone did not preserve nil or empty configuration: %+v", rules)
		}
	}
}

func TestMissionRulesJSON(t *testing.T) {
	type definition struct {
		Rules missions.RuleSet `json:"rules"`
	}
	const input = `{
		"rules": {
			"aggregate-results": {"actions": ["batch-ports"]},
			"decide-next-actions": {
				"actions": ["scan-ports"],
				"params": {"decision_by": "model"}
			}
		}
	}`
	var decoded definition
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Rules.Validate(); err != nil {
		t.Fatal(err)
	}
	want := missions.RuleSet{
		missions.AggregateResults:  {Actions: []string{"batch-ports"}},
		missions.DecideNextActions: {Actions: []string{"scan-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
	if !reflect.DeepEqual(decoded.Rules, want) {
		t.Fatalf("decoded rules = %+v, want %+v", decoded.Rules, want)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var originalJSON, encodedJSON any
	if err := json.Unmarshal([]byte(input), &originalJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &encodedJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(originalJSON, encodedJSON) {
		t.Fatalf("JSON roundtrip changed configuration: %s", encoded)
	}
}
