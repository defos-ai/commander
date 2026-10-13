package mcp_test

import (
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestMissions(t *testing.T) {
	a := connect(t)
	var catalog struct {
		Missions []map[string]string `json:"missions"`
	}
	a.call("list_missions", object{}, &catalog)
	if len(catalog.Missions) != 2 {
		t.Fatalf("unexpected missions: %+v", catalog)
	}
	for i, expected := range []struct {
		name, discovery string
		checks          []string
	}{
		{"review-website-no-browser", "discover-pages-no-browser", []string{"check-links", "check-seo", "detect-technologies"}},
		{"review-website-with-browser", "discover-pages", []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"}},
	} {
		summary := catalog.Missions[i]
		if summary["name"] != expected.name || len(summary) != 2 || summary["objective"] == "" {
			t.Fatalf("expected mission name and objective only: %+v", summary)
		}
		var mission missions.Mission
		a.call("describe_mission", object{"mission_name": summary["name"]}, &mission)
		if mission.Name != summary["name"] || mission.Objective != summary["objective"] {
			t.Fatalf("mission detail differs from summary: %+v", mission)
		}
		if !reflect.DeepEqual(mission.StartingActions, []string{expected.discovery}) || len(mission.RequiredActions) != 1+len(expected.checks) {
			t.Fatalf("incorrect website workflow: %+v", mission)
		}
		aggregate := mission.Rules[missions.AggregateResults]
		decision := mission.Rules[missions.DecideNextActions]
		if len(mission.Rules) != 2 || !reflect.DeepEqual(aggregate.Actions, []string{expected.discovery}) || aggregate.Params != nil || !reflect.DeepEqual(decision.Actions, expected.checks) || decision.Params == nil || decision.Params.DecisionBy != missions.Model {
			t.Fatalf("mission detail lost grouping or model rules: %+v", mission.Rules)
		}
		if len(mission.InputSchema) == 0 || len(mission.OutputSchema) == 0 {
			t.Fatal("mission schemas missing")
		}
	}
	a.fails("describe_mission", object{"mission_name": "missing"}, "unknown")
	a.fails("start_operation", object{"mission_name": "missing", "input": object{}}, "unknown")
	a.fails("start_operation", object{"mission_name": "review-website-with-browser", "input": object{"url": "file:///etc/passwd"}}, "input")
	opID := id.OperationID()
	args := object{"operation_id": opID, "mission_name": "review-website-with-browser", "input": object{"url": "https://example.test"}}
	var op operations.Operation
	a.call("start_operation", args, &op)
	if op.ID != opID || op.Status != operations.Planning {
		t.Fatalf("unexpected start: %+v", op)
	}
	a.fails("start_operation", args, "conflict")
	a.call("cancel_operation", object{"operation_id": opID}, nil)
}
