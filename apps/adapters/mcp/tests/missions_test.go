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
	for _, name := range []string{"hacker-news-test", "build-agent", "fixture-use-agent", "install-agent"} {
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
	summary := summaries["hacker-news-test"]
	var raw json.RawMessage
	a.Call("describe_mission", fixtures.Object{"mission_name": "hacker-news-test"}, &raw)
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
	if !slices.Equal(mission.StartingActions, []string{"rank-hn-stories"}) || !slices.Equal(mission.RequiredActions, []string{"rank-hn-stories", "write-hn-digest"}) {
		t.Fatalf("incorrect fixture workflow: %+v", mission)
	}
	if mission.Policies == nil || mission.Policies.Continuation == nil || !slices.Equal(mission.Policies.Continuation.AggregateResults, []string{"rank-hn-stories"}) || !slices.Equal(mission.Policies.Continuation.ModelReview, []string{"write-hn-digest"}) {
		t.Fatalf("mission detail lost continuation policies: %+v", mission.Policies)
	}

	if len(mission.InputSchema) == 0 || len(mission.OutputSchema) == 0 {
		t.Fatal("mission schemas missing")
	}
	a.Fails("describe_mission", fixtures.Object{"mission_name": "missing"}, "unknown")
	a.Fails("start_operation", fixtures.Object{"mission_name": "missing", "input": fixtures.Object{}}, "unknown")
	a.Fails("start_operation", fixtures.Object{"mission_name": "hacker-news-test", "input": fixtures.Object{}}, "input")
	opID := id.OperationID()
	args := fixtures.Object{"operation_id": opID, "mission_name": "hacker-news-test", "input": fixtures.StoryBatch("Explicit operation identity")}
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
		t.Fatal("MCP mission creation or detail lost mission policies")
	}
	var first operations.Operation
	a.Call("start_operation", fixtures.Object{"mission_name": definition.Name, "input": fixtures.Object{}}, &first)
	if !reflect.DeepEqual(first.Mission.Policies, created.Policies) || len(first.Tasks) != 0 {
		t.Fatal("operation lost mission policies or policies started work")
	}
	definition.Policies.Completion.DecisionBy = policies.Model
	definition.Policies.Continuation.AggregateResults = []string{"review-vulnerabilities"}
	definition.Policies.Continuation.ModelReview = []string{"review-vulnerabilities"}
	definition.Policies.Sharing[0].ToActions[0] = "review-vulnerabilities"
	definition.Policies.Sharing[0].Include = []policies.SharingKind{policies.ShareDecisions}
	a.Call("update_mission", fixtures.MissionDefinition(definition), &updated)
	a.Call("describe_mission", fixtures.Object{"mission_name": definition.Name}, &described)
	if !reflect.DeepEqual(updated.Policies, definition.Policies) || !reflect.DeepEqual(described.Policies, definition.Policies) {
		t.Fatal("MCP mission update or detail lost mission policies")
	}
	if !reflect.DeepEqual(a.Get(first.ID).Mission.Policies, created.Policies) {
		t.Fatal("mission update changed an existing operation's policies")
	}
	var second operations.Operation
	a.Call("start_operation", fixtures.Object{"mission_name": definition.Name, "input": fixtures.Object{}}, &second)
	if !reflect.DeepEqual(second.Mission.Policies, definition.Policies) {
		t.Fatal("new operation did not retain updated policies")
	}

	invalid := fixtures.MissionDefinition(definition)
	invalid["policies"] = fixtures.Object{"sharing": []fixtures.Object{{"from_actions": []string{"find-vulnerabilities"}, "to_actions": []string{"analyze-vulnerabilities"}, "include": []string{"secrets"}}}}
	invalidClient := fixtures.OpenSession(t)
	_, err := invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: invalid})
	if err == nil || !strings.Contains(err.Error(), "include") {
		t.Fatalf("unsupported sharing kind should be an invalid argument: %v", err)
	}
	for _, removed := range []any{nil, fixtures.Object{}} {
		candidate := fixtures.MissionDefinition(definition)
		candidate["policies"] = fixtures.Object{"placement": removed}
		_, err = invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: candidate})
		if err == nil {
			t.Fatal("removed placement policy must be rejected")
		}
	}
	for _, continuation := range fixtures.InvalidContinuationPolicies() {
		candidate := fixtures.MissionDefinition(definition)
		candidate["policies"] = fixtures.Object{"continuation": continuation}
		_, err = invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: candidate})
		if err == nil {
			t.Fatalf("invalid continuation should be an invalid argument: %+v", continuation)
		}
	}
	for _, completion := range fixtures.InvalidCompletionPolicies() {
		candidate := fixtures.MissionDefinition(definition)
		candidate["policies"] = fixtures.Object{"completion": completion}
		_, err = invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: candidate})
		if err == nil {
			t.Fatalf("invalid completion should be an invalid argument: %+v", completion)
		}
	}
	for _, session := range []any{nil, []fixtures.Object{}, []fixtures.Object{{"create_action": "open-assessment", "use_actions": []string{"analyze-vulnerabilities"}}}} {
		candidate := fixtures.MissionDefinition(definition)
		candidate["policies"] = fixtures.Object{"session": session}
		for _, tool := range []string{"create_mission", "update_mission"} {
			_, err = invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: tool, Arguments: candidate})
			if err == nil {
				t.Fatalf("%s accepted removed session policy: %+v", tool, session)
			}
		}
	}
	a.Call("describe_mission", fixtures.Object{"mission_name": definition.Name}, &described)
	if !reflect.DeepEqual(described.Policies, definition.Policies) {
		t.Fatal("removed session policies changed the saved mission")
	}
	for _, completion := range []any{nil, fixtures.Object{}, fixtures.Object{"decision_by": "operator"}} {
		legacy := fixtures.MissionDefinition(definition)
		legacy["completion"] = completion
		_, err = invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: legacy})
		if err == nil {
			t.Fatalf("legacy root completion must be rejected: %+v", completion)
		}
	}
	for _, rules := range []any{nil, fixtures.Object{}, fixtures.Object{"aggregate-results": fixtures.Object{"for_actions": []string{"find-vulnerabilities"}}}, fixtures.Object{"decide-next-actions": fixtures.Object{"for_actions": []string{"find-vulnerabilities"}, "params": fixtures.Object{"decision_by": "model"}}}} {
		legacy := fixtures.MissionDefinition(definition)
		legacy["rules"] = rules
		_, err = invalidClient.Session.CallTool(invalidClient.Context, &mcp.CallToolParams{Name: "update_mission", Arguments: legacy})
		if err == nil {
			t.Fatalf("legacy root rules must be rejected: %+v", rules)
		}
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
	for _, continuation := range []any{nil, fixtures.Object{}, fixtures.Object{"aggregate_results": nil, "model_review": nil}, fixtures.Object{"aggregate_results": []string{}, "model_review": []string{}}} {
		candidate := fixtures.MissionDefinition(definition)
		candidate["policies"] = fixtures.Object{"continuation": continuation}
		updated = missions.Mission{}
		a.Call("update_mission", candidate, &updated)
		if updated.Policies == nil || continuation == nil && updated.Policies.Continuation != nil || continuation != nil && (updated.Policies.Continuation == nil || len(updated.Policies.Continuation.AggregateResults) != 0 || len(updated.Policies.Continuation.ModelReview) != 0) {
			t.Fatal("empty continuation should declare no overrides")
		}
	}
	candidate := fixtures.MissionDefinition(definition)
	candidate["policies"] = fixtures.Object{"completion": nil}
	updated = missions.Mission{}
	a.Call("update_mission", candidate, &updated)
	if updated.Policies == nil || updated.Policies.Completion != nil {
		t.Fatal("null completion must remain absent")
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
