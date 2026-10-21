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
			missions.AggregateResults: {ForActions: []string{"batch-ports"}},
		}},
		{name: "aggregate empty parameters", rules: missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{"batch-ports"}, Params: &missions.RuleParams{}},
		}},
		{name: "same-agent defaults", rules: missions.RuleSet{
			missions.SameAgent: {ForActions: []string{"checkout-template", "build-agent"}},
		}},
		{name: "same-agent empty parameters", rules: missions.RuleSet{
			missions.SameAgent: {ForActions: []string{"checkout-template", "build-agent"}, Params: &missions.RuleParams{}},
		}},
		{name: "composable rules on same action", rules: missions.RuleSet{
			missions.AggregateResults:  {ForActions: []string{"batch-ports"}},
			missions.DecideNextActions: {ForActions: []string{"batch-ports", "scan-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
			missions.SameAgent:         {ForActions: []string{"batch-ports", "scan-ports"}},
		}},
		{name: "unknown rule", rules: missions.RuleSet{
			"unknown": {ForActions: []string{"scan-ports"}},
		}, want: `rule "unknown": unsupported rule`},
		{name: "empty selectors", rules: missions.RuleSet{
			missions.SameAgent: {},
		}, want: "for_actions must not be empty"},
		{name: "blank selector", rules: missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{"batch-ports", " \t"}},
		}, want: "nonblank names"},
		{name: "leading whitespace", rules: missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{" batch-ports"}},
		}, want: "without surrounding whitespace"},
		{name: "trailing whitespace", rules: missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{"batch-ports\n"}},
		}, want: "without surrounding whitespace"},
		{name: "duplicate selector", rules: missions.RuleSet{
			missions.SameAgent: {ForActions: []string{"batch-ports", "batch-ports"}},
		}, want: `duplicate action "batch-ports"`},
		{name: "aggregation decision parameters", rules: missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{"batch-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		}, want: "does not accept decision_by"},
		{name: "same-agent decision parameters", rules: missions.RuleSet{
			missions.SameAgent: {ForActions: []string{"batch-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		}, want: "does not accept decision_by"},
		{name: "missing decision parameters", rules: missions.RuleSet{
			missions.DecideNextActions: {ForActions: []string{"scan-ports"}},
		}, want: `decision_by must be "model"`},
		{name: "empty decision", rules: missions.RuleSet{
			missions.DecideNextActions: {ForActions: []string{"scan-ports"}, Params: &missions.RuleParams{}},
		}, want: `decision_by must be "model"`},
		{name: "unsupported decision", rules: missions.RuleSet{
			missions.DecideNextActions: {ForActions: []string{"scan-ports"}, Params: &missions.RuleParams{DecisionBy: "other"}},
		}, want: `decision_by must be "model"`},
		{name: "stable first error", rules: missions.RuleSet{
			missions.DecideNextActions: {},
			missions.AggregateResults:  {},
		}, want: `rule "aggregate-results": for_actions must not be empty`},
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
		missions.AggregateResults:  {ForActions: []string{"batch-ports"}},
		missions.DecideNextActions: {ForActions: []string{"scan-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
	cloned := original.Clone()
	if !reflect.DeepEqual(original, cloned) {
		t.Fatalf("clone changed configuration: %+v", cloned)
	}
	cloned[missions.AggregateResults].ForActions[0] = "other-batch"
	cloned[missions.DecideNextActions].ForActions[0] = "other-scan"
	cloned[missions.DecideNextActions].Params.DecisionBy = "other"
	delete(cloned, missions.AggregateResults)
	if original[missions.AggregateResults].ForActions[0] != "batch-ports" ||
		original[missions.DecideNextActions].ForActions[0] != "scan-ports" ||
		original[missions.DecideNextActions].Params.DecisionBy != missions.Model {
		t.Fatalf("editing clone changed original: %+v", original)
	}
	for _, rules := range []missions.RuleSet{nil, {}, {missions.AggregateResults: {}}, {missions.AggregateResults: {ForActions: []string{}}}} {
		if !reflect.DeepEqual(rules, rules.Clone()) {
			t.Fatalf("clone did not preserve nil or empty configuration: %+v", rules)
		}
	}
}

func TestMissionRulesJSON(t *testing.T) {
	const input = `{
		"name": "scan",
		"objective": "Review network services",
		"required_actions": ["batch-ports", "scan-ports"],
		"optional_actions": ["report-scan"],
		"starting_actions": ["batch-ports"],
		"input_schema": {},
		"output_schema": {},
		"rules": {
			"aggregate-results": {"for_actions": ["batch-ports"]},
			"decide-next-actions": {
				"for_actions": ["scan-ports"],
				"params": {"decision_by": "model"}
			},
			"same-agent": {"for_actions": ["batch-ports", "scan-ports"]}
		}
	}`
	decoded, err := missions.LoadMission([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	want := missions.RuleSet{
		missions.AggregateResults:  {ForActions: []string{"batch-ports"}},
		missions.DecideNextActions: {ForActions: []string{"scan-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		missions.SameAgent:         {ForActions: []string{"batch-ports", "scan-ports"}},
	}
	if !reflect.DeepEqual(decoded.Rules, want) {
		t.Fatalf("decoded rules = %+v, want %+v", decoded.Rules, want)
	}
	cloned := decoded.Clone()
	cloned.OptionalActions[0] = "changed-by-reader"
	if !reflect.DeepEqual(decoded.OptionalActions, []string{"report-scan"}) {
		t.Fatal("mission clone shares optional action names")
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

func TestMissionLoadingRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
		want   string
	}{
		{"legacy automations", `"automations": {"batch-ports": {"run": ["scan-ports"], "continue": "when_all"}}`, `unknown field "automations"`},
		{"unknown rule", `"rules": {"invented": {"for_actions": ["batch-ports"]}}`, `rule "invented": unsupported rule`},
		{"old selector", `"rules": {"aggregate-results": {"actions": ["batch-ports"]}}`, `unknown field "actions"`},
		{"unknown parameters", `"rules": {"decide-next-actions": {"for_actions": ["scan-ports"], "params": {"decision_by": "model", "extra": true}}}`, `unknown field "extra"`},
		{"unsupported decision", `"rules": {"decide-next-actions": {"for_actions": ["scan-ports"], "params": {"decision_by": "other"}}}`, `decision_by must be "model"`},
		{"duplicate required action", `"required_actions": ["scan-ports", "scan-ports"]`, `required_actions contains duplicate action`},
		{"duplicate optional action", `"optional_actions": ["report-scan", "report-scan"]`, `optional_actions contains duplicate action`},
		{"blank optional action", `"optional_actions": [" "]`, `optional_actions must contain nonblank names`},
		{"untrimmed optional action", `"optional_actions": [" report-scan"]`, `without surrounding whitespace`},
		{"overlapping action lists", `"required_actions": ["scan-ports"], "optional_actions": ["scan-ports"]`, `must not be both required and optional`},
		{"optional starting action", `"optional_actions": ["report-scan"], "starting_actions": ["report-scan"]`, `is not a required action`},
		{"duplicate starting action", `"required_actions": ["scan-ports"], "starting_actions": ["scan-ports", "scan-ports"]`, `starting_actions contains duplicate action`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `{"name":"scan","objective":"Review network services","input_schema":{},"output_schema":{},` + tc.fields + `}`
			_, err := missions.LoadMission([]byte(input))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadMission() = %v, want %q", err, tc.want)
			}
		})
	}
}
