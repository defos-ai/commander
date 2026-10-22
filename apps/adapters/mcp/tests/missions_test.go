package mcp_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestMissions(t *testing.T) {
	a := connect(t)
	var catalog struct {
		Missions []map[string]any `json:"missions"`
	}
	a.call("list_missions", object{}, &catalog)
	if len(catalog.Missions) != 7 {
		t.Fatalf("unexpected missions: %+v", catalog)
	}
	summaries := make(map[string]map[string]any, len(catalog.Missions))
	for _, summary := range catalog.Missions {
		name, named := summary["name"].(string)
		objectives, listed := summary["objectives"].([]any)
		if len(summary) != 2 || !named || name == "" || !listed || len(objectives) == 0 {
			t.Fatalf("expected mission name and objectives only: %+v", summary)
		}
		summaries[name] = summary
	}
	for _, name := range []string{"analyze-text", "build-agent", "fixture-affinity", "fixture-use-agent", "install-agent"} {
		if summaries[name] == nil {
			t.Fatalf("missing agent mission: %s", name)
		}
	}
	var build missions.Mission
	a.call("describe_mission", object{"mission_name": "build-agent"}, &build)
	if !slices.Equal(build.RequiredActions, []string{"checkout-template", "build-agent"}) || !slices.Equal(build.OptionalActions, []string{"publish-agent", "release-agent"}) {
		t.Fatalf("build mission lost its optional publication and release actions: %+v", build)
	}
	var buildOperation operations.Operation
	a.call("start_operation", object{"mission_name": "build-agent", "input": object{
		"instructions": "Create a fixture agent",
		"actions":      []object{{"name": "inspect", "description": "Inspect supplied input.", "input_schema": object{"type": "object"}, "output_schema": object{"type": "object"}}},
	}}, &buildOperation)
	if buildOperation.Preflight == nil || len(buildOperation.Preflight.Actions) != 2 {
		t.Fatalf("build preflight included optional publication: %+v", buildOperation.Preflight)
	}
	for _, action := range buildOperation.Preflight.Actions {
		if !slices.Contains(build.RequiredActions, action.ActionName) {
			t.Fatalf("unscheduled optional action blocked build preflight: %+v", action)
		}
	}
	a.call("cancel_operation", object{"operation_id": buildOperation.ID}, nil)
	summary := summaries["analyze-text"]
	var raw json.RawMessage
	a.call("describe_mission", object{"mission_name": "analyze-text"}, &raw)
	mission, err := missions.LoadMission(raw)
	if err != nil {
		t.Fatalf("mission detail does not match the mission definition format: %v", err)
	}
	if mission.Name != summary["name"] || len(mission.Objectives) != len(summary["objectives"].([]any)) {
		t.Fatalf("mission detail differs from summary: %+v", mission)
	}
	for index, objective := range mission.Objectives {
		if objective != summary["objectives"].([]any)[index] {
			t.Fatalf("mission detail objectives differ from summary: %+v", mission)
		}
	}
	if !slices.Equal(mission.StartingActions, []string{"analyze-text"}) || !slices.Equal(mission.RequiredActions, []string{"analyze-text", "write-summary"}) {
		t.Fatalf("incorrect fixture workflow: %+v", mission)
	}
	aggregate := mission.Rules[missions.AggregateResults]
	decision := mission.Rules[missions.DecideNextActions]
	if len(mission.Rules) != 2 || !reflect.DeepEqual(aggregate.ForActions, []string{"analyze-text"}) || aggregate.Params != nil || !reflect.DeepEqual(decision.ForActions, []string{"write-summary"}) || decision.Params == nil || decision.Params.DecisionBy != missions.Model {
		t.Fatalf("mission detail lost grouping or model rules: %+v", mission.Rules)
	}
	if len(mission.InputSchema) == 0 || len(mission.OutputSchema) == 0 {
		t.Fatal("mission schemas missing")
	}
	a.fails("describe_mission", object{"mission_name": "missing"}, "unknown")
	a.fails("start_operation", object{"mission_name": "missing", "input": object{}}, "unknown")
	a.fails("start_operation", object{"mission_name": "analyze-text", "input": object{"text": ""}}, "input")
	opID := id.OperationID()
	args := object{"operation_id": opID, "mission_name": "analyze-text", "input": object{"text": "Explicit operation identity"}}
	var op operations.Operation
	a.call("start_operation", args, &op)
	if op.ID != opID || op.Status != operations.Planning {
		t.Fatalf("unexpected start: %+v", op)
	}
	a.fails("start_operation", args, "conflict")
	a.call("cancel_operation", object{"operation_id": opID}, nil)
}
