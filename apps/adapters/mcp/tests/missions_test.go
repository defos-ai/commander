package mcp_test

import (
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"testing"
)

func TestMissions(t *testing.T) {
	a := connect(t)
	var catalog struct {
		Missions []map[string]string `json:"missions"`
	}
	a.call("list_missions", object{}, &catalog)
	if len(catalog.Missions) != 1 || catalog.Missions[0]["name"] != "review-website" {
		t.Fatalf("unexpected missions: %+v", catalog)
	}
	summary := catalog.Missions[0]
	if len(summary) != 2 || summary["objective"] == "" {
		t.Fatalf("expected only mission name and objective: %+v", summary)
	}
	var mission missions.Mission
	a.call("describe_mission", object{"mission_name": summary["name"]}, &mission)
	if mission.Name != summary["name"] || mission.Objective != summary["objective"] {
		t.Fatalf("mission detail differs from summary: %+v", mission)
	}
	a.fails("describe_mission", object{"mission_name": "missing"}, "unknown")
	if len(mission.StartingActions) != 1 || mission.StartingActions[0] != "discover-pages" || len(mission.Automations["discover-pages"].Run) != 5 {
		t.Fatalf("missing automatic website workflow: %+v", mission)
	}
	if len(mission.InputSchema) == 0 || len(mission.OutputSchema) == 0 {
		t.Fatal("mission schemas missing")
	}
	a.fails("start_operation", object{"mission_name": "missing", "input": object{}}, "unknown")
	a.fails("start_operation", object{"mission_name": mission.Name, "input": object{"url": "file:///etc/passwd"}}, "input")
	opID := id.OperationID()
	args := object{"operation_id": opID, "mission_name": mission.Name, "input": object{"url": "https://example.test"}}
	var op operations.Operation
	a.call("start_operation", args, &op)
	if op.ID != opID || op.Status != operations.Planning {
		t.Fatalf("unexpected start: %+v", op)
	}
	a.fails("start_operation", args, "conflict")
	a.call("cancel_operation", object{"operation_id": opID}, nil)
}
