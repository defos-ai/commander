package runner_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestMissionActionDefinitionsRemainIndependentPlanningContracts(t *testing.T) {
	mission := missions.Mission{
		Name: "future-review", Objectives: []string{"Review with capabilities implemented later."},
		RequiredActions: []string{"inspect"}, OptionalActions: []string{"report"},
		Steps:       []missions.Step{{ID: "review", Objective: "Check results", RequiredActions: []string{"verify"}}},
		InputSchema: json.RawMessage("{}"), OutputSchema: json.RawMessage("{}"),
	}
	for _, name := range []string{"inspect", "report", "verify"} {
		mission.ActionDefinitions = append(mission.ActionDefinitions, actions.Definition{
			Name: name, RequiredSecrets: []string{"SERVICE_TOKEN"},
			InputSchema:  json.RawMessage(`{"type":"object","$defs":{"exact":{"const":9007199254740993}},"properties":{"id":{"$ref":"#/$defs/exact"}}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		})
	}
	data, err := json.Marshal(mission)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := missions.LoadMission(data)
	if err != nil {
		t.Fatal(err)
	}
	cloned := saved.Clone()
	cloned.ActionDefinitions[0].RequiredSecrets[0] = "OTHER_TOKEN"
	cloned.ActionDefinitions[0].InputSchema[0] = '['
	cloned.ActionDefinitions[0].OutputSchema[0] = '['
	if !reflect.DeepEqual(saved, mission) || !strings.Contains(string(saved.ActionDefinitions[0].InputSchema), "9007199254740993") {
		t.Fatal("mission definitions changed during storage roundtrip or share mutable clone data")
	}
	preflight, err := (operations.Operation{Mission: saved}).AssessPreflight(nil)
	if err != nil || preflight.Ready || len(preflight.Actions) != 2 {
		t.Fatalf("definitions supplied readiness without registered agents: preflight=%+v error=%v", preflight, err)
	}
}

func TestMissionActionDefinitionsRejectInvalidContracts(t *testing.T) {
	base := missions.Mission{
		Name: "review", Objectives: []string{"Inspect supplied data."}, RequiredActions: []string{"inspect"},
		InputSchema: json.RawMessage("{}"), OutputSchema: json.RawMessage("{}"),
		ActionDefinitions: []actions.Definition{{Name: "inspect",
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	for _, tc := range []struct {
		name   string
		change func(*missions.Mission)
		want   string
	}{
		{"duplicate definitions", func(m *missions.Mission) {
			m.ActionDefinitions = append(m.ActionDefinitions, m.ActionDefinitions[0].Clone())
		}, "duplicate action"},
		{"unreferenced definition", func(m *missions.Mission) { m.ActionDefinitions[0].Name = "unused" }, "must reference"},
		{"untrimmed name", func(m *missions.Mission) { m.ActionDefinitions[0].Name = " inspect" }, "without surrounding whitespace"},
		{"invalid secret", func(m *missions.Mission) { m.ActionDefinitions[0].RequiredSecrets = []string{"TOKEN=value"} }, "required secret"},
		{"implicit root", func(m *missions.Mission) { m.ActionDefinitions[0].InputSchema = json.RawMessage("{}") }, "type object"},
		{"non-object output", func(m *missions.Mission) { m.ActionDefinitions[0].OutputSchema = json.RawMessage(`{"type":"string"}`) }, "output_schema"},
		{"invalid nested type", func(m *missions.Mission) {
			m.ActionDefinitions[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"invented"}}}`)
		}, "invalid schema type"},
		{"invalid default", func(m *missions.Mission) {
			m.ActionDefinitions[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","default":"wrong"}}}`)
		}, "input_schema"},
		{"unresolved local reference", func(m *missions.Mission) {
			m.ActionDefinitions[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"id":{"$ref":"#/$defs/missing"}}}`)
		}, "input_schema"},
		{"external reference", func(m *missions.Mission) {
			m.ActionDefinitions[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"id":{"$ref":"https://example.test/schema"}}}`)
		}, "input_schema"},
		{"too many definitions", func(m *missions.Mission) {
			for len(m.ActionDefinitions) <= 100 {
				m.ActionDefinitions = append(m.ActionDefinitions, m.ActionDefinitions[0].Clone())
			}
		}, "at most 100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mission := base.Clone()
			tc.change(&mission)
			if err := mission.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v; want %q", err, tc.want)
			}
		})
	}
}
