package mcp_test

import (
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	"testing"
)

func TestMissions(t *testing.T) {
	// Arrange: use the mission loaded by the real controller's --with-missions.
	a := connect(t)
	var catalog struct {
		Missions []missions.Mission `json:"missions"`
	}
	// Act.
	a.call("list_missions", object{}, &catalog)
	// Assert: the definition is usable by an external client, including automation.
	if len(catalog.Missions) != 1 || catalog.Missions[0].Name != "review-website" {
		t.Fatalf("unexpected missions: %+v", catalog)
	}
	mission := catalog.Missions[0]
	if len(mission.StartingActions) != 1 || mission.StartingActions[0] != "discover-pages" || len(mission.Automations["discover-pages"].Run) != 5 {
		t.Fatalf("missing automatic website workflow: %+v", mission)
	}
	if len(mission.InputSchema) == 0 || len(mission.OutputSchema) == 0 {
		t.Fatal("mission schemas missing")
	}
	a.fails("start_operation", object{"mission_name": "missing", "input": object{}}, "unknown")
	a.fails("start_operation", object{"mission_name": mission.Name, "input": object{"url": "file:///etc/passwd"}}, "input")
	// A failed request must not break the MCP session; callers can supply their ID.
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
