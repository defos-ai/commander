package mcp_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMissions(t *testing.T) {
	a := fixtures.Connect(t)
	var catalog struct {
		Missions []map[string]any `json:"missions"`
	}
	a.Call("list_missions", fixtures.Object{}, &catalog)
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
	a.Call("describe_mission", fixtures.Object{"mission_name": "build-agent"}, &build)
	if !slices.Equal(build.RequiredActions, []string{"checkout-template", "build-agent"}) || !slices.Equal(build.OptionalActions, []string{"publish-agent", "release-agent"}) {
		t.Fatalf("build mission lost its optional publication and release actions: %+v", build)
	}
	var buildOperation operations.Operation
	a.Call("start_operation", fixtures.Object{"mission_name": "build-agent", "input": fixtures.Object{
		"instructions": "Create a fixture agent",
		"actions":      []fixtures.Object{{"name": "inspect", "description": "Inspect supplied input.", "input_schema": fixtures.Object{"type": "object"}, "output_schema": fixtures.Object{"type": "object"}}},
	}}, &buildOperation)
	if buildOperation.Preflight == nil || len(buildOperation.Preflight.Actions) != 2 {
		t.Fatalf("build preflight included optional publication: %+v", buildOperation.Preflight)
	}
	for _, action := range buildOperation.Preflight.Actions {
		if !slices.Contains(build.RequiredActions, action.ActionName) {
			t.Fatalf("unscheduled optional action blocked build preflight: %+v", action)
		}
	}
	a.Call("cancel_operation", fixtures.Object{"operation_id": buildOperation.ID}, nil)
	summary := summaries["analyze-text"]
	var raw json.RawMessage
	a.Call("describe_mission", fixtures.Object{"mission_name": "analyze-text"}, &raw)
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
	a.Fails("describe_mission", fixtures.Object{"mission_name": "missing"}, "unknown")
	a.Fails("start_operation", fixtures.Object{"mission_name": "missing", "input": fixtures.Object{}}, "unknown")
	a.Fails("start_operation", fixtures.Object{"mission_name": "analyze-text", "input": fixtures.Object{"text": ""}}, "input")
	opID := id.OperationID()
	args := fixtures.Object{"operation_id": opID, "mission_name": "analyze-text", "input": fixtures.Object{"text": "Explicit operation identity"}}
	var op operations.Operation
	a.Call("start_operation", args, &op)
	if op.ID != opID || op.Status != operations.Planning {
		t.Fatalf("unexpected start: %+v", op)
	}
	a.Fails("start_operation", args, "conflict")
	a.Call("cancel_operation", fixtures.Object{"operation_id": opID}, nil)
}

func TestMissionContextSharingPolicy(t *testing.T) {
	a := fixtures.Connect(t)
	definition := fixtures.ContextMission()
	var created, described, updated missions.Mission

	a.Call("create_mission", fixtures.MissionDefinition(definition), &created)
	a.Call("describe_mission", fixtures.Object{"mission_name": definition.Name}, &described)
	if !reflect.DeepEqual(created.Context, definition.Context) || !reflect.DeepEqual(described.Context, definition.Context) {
		t.Fatal("MCP mission creation or detail lost the sharing policy")
	}
	definition.Context.Sharing[0].ToActions[0] = "review-vulnerabilities"
	definition.Context.Sharing[0].Include = []missions.ContextKind{missions.ContextDecisions}
	a.Call("update_mission", fixtures.MissionDefinition(definition), &updated)
	a.Call("describe_mission", fixtures.Object{"mission_name": definition.Name}, &described)
	if !reflect.DeepEqual(updated.Context, definition.Context) || !reflect.DeepEqual(described.Context, definition.Context) {
		t.Fatal("MCP mission update or detail lost the sharing policy")
	}

	invalid := fixtures.MissionDefinition(definition)
	invalid["context"] = fixtures.Object{"sharing": []fixtures.Object{{"from_actions": []string{"find-vulnerabilities"}, "to_actions": []string{"analyze-vulnerabilities"}, "include": []string{"secrets"}}}}
	invalidClient := fixtures.OpenSession(t)
	_, err := invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: invalid})
	if err == nil || !strings.Contains(err.Error(), "include") {
		t.Fatalf("unsupported sharing kind should be an invalid argument: %v", err)
	}
	definition.Context = nil
	updated, described = missions.Mission{}, missions.Mission{}
	a.Call("update_mission", fixtures.MissionDefinition(definition), &updated)
	a.Call("describe_mission", fixtures.Object{"mission_name": definition.Name}, &described)
	if updated.Context != nil || described.Context != nil {
		t.Fatal("null context policy was not removed")
	}
}
