package mcp_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
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
	if len(mission.Rules) != 2 || !reflect.DeepEqual(aggregate.ForActions, []string{"analyze-text"}) || aggregate.Params != nil || !reflect.DeepEqual(decision.ForActions, []string{"write-summary"}) || decision.Params == nil || decision.Params.DecisionBy != policies.Model {
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

func TestMissionSharingPolicy(t *testing.T) {
	a := fixtures.Connect(t)
	definition := fixtures.SharingMission()
	var created, described, updated missions.Mission

	a.Call("create_mission", fixtures.MissionDefinition(definition), &created)
	a.Call("describe_mission", fixtures.Object{"mission_name": definition.Name}, &described)
	if !reflect.DeepEqual(created.Policies, definition.Policies) || !reflect.DeepEqual(described.Policies, definition.Policies) {
		t.Fatal("MCP mission creation or detail lost the sharing policy")
	}
	var first operations.Operation
	a.Call("start_operation", fixtures.Object{"mission_name": definition.Name, "input": fixtures.Object{}}, &first)
	if !reflect.DeepEqual(first.Mission.Policies, created.Policies) || len(first.Tasks) != 0 {
		t.Fatal("operation lost mission policies or policies started work")
	}
	definition.Policies.Placement.SameAgent[1] = "review-vulnerabilities"
	definition.Policies.Sharing[0].ToActions[0] = "review-vulnerabilities"
	definition.Policies.Sharing[0].Include = []policies.SharingKind{policies.ShareDecisions}
	a.Call("update_mission", fixtures.MissionDefinition(definition), &updated)
	a.Call("describe_mission", fixtures.Object{"mission_name": definition.Name}, &described)
	if !reflect.DeepEqual(updated.Policies, definition.Policies) || !reflect.DeepEqual(described.Policies, definition.Policies) {
		t.Fatal("MCP mission update or detail lost the sharing policy")
	}
	if !reflect.DeepEqual(a.Get(first.ID).Mission.Policies, created.Policies) {
		t.Fatal("mission update changed an existing operation's policies")
	}
	var second operations.Operation
	a.Call("start_operation", fixtures.Object{"mission_name": definition.Name, "input": fixtures.Object{}}, &second)
	if !reflect.DeepEqual(second.Mission.Policies, definition.Policies) {
		t.Fatal("new operation did not retain updated placement and sharing policies")
	}

	invalid := fixtures.MissionDefinition(definition)
	invalid["policies"] = fixtures.Object{"sharing": []fixtures.Object{{"from_actions": []string{"find-vulnerabilities"}, "to_actions": []string{"analyze-vulnerabilities"}, "include": []string{"secrets"}}}}
	invalidClient := fixtures.OpenSession(t)
	_, err := invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: invalid})
	if err == nil || !strings.Contains(err.Error(), "include") {
		t.Fatalf("unsupported sharing kind should be an invalid argument: %v", err)
	}
	for _, placement := range fixtures.InvalidPlacementPolicies() {
		candidate := fixtures.MissionDefinition(definition)
		candidate["policies"] = fixtures.Object{"placement": placement}
		_, err = invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: candidate})
		if err == nil {
			t.Fatalf("invalid placement should be an invalid argument: %+v", placement)
		}
	}
	legacyRule := fixtures.MissionDefinition(definition)
	legacyRule["rules"] = fixtures.Object{"same-agent": fixtures.Object{"for_actions": []string{"find-vulnerabilities"}}}
	_, err = invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: legacyRule})
	if err == nil {
		t.Fatal("same-agent must be rejected as a rule")
	}
	for _, field := range []string{"context", "sharing"} {
		legacy := fixtures.MissionDefinition(definition)
		delete(legacy, "policies")
		legacy[field] = definition.Policies.Sharing
		if field == "context" {
			legacy[field] = fixtures.Object{"sharing": definition.Policies.Sharing}
		}
		legacyClient := fixtures.OpenSession(t)
		_, err = legacyClient.Session.CallTool(legacyClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: legacy})
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Fatalf("old root %s field should be rejected: %v", field, err)
		}
	}
	for _, placement := range []any{nil, fixtures.Object{}, fixtures.Object{"same_agent": nil}, fixtures.Object{"same_agent": []string{}}} {
		candidate := fixtures.MissionDefinition(definition)
		candidate["policies"] = fixtures.Object{"placement": placement}
		updated = missions.Mission{}
		a.Call("update_mission", candidate, &updated)
		if updated.Policies == nil || placement == nil && updated.Policies.Placement != nil || placement != nil && (updated.Policies.Placement == nil || len(updated.Policies.Placement.SameAgent) != 0) {
			t.Fatal("empty placement should declare no affinity group")
		}
	}
	definition.Policies = nil
	updated, described = missions.Mission{}, missions.Mission{}
	a.Call("update_mission", fixtures.MissionDefinition(definition), &updated)
	a.Call("describe_mission", fixtures.Object{"mission_name": definition.Name}, &described)
	if updated.Policies != nil || described.Policies != nil {
		t.Fatal("null policies were not removed")
	}
	for _, operation := range []operations.Operation{first, second} {
		a.CancelActiveOperation(operation.ID)
	}
}
