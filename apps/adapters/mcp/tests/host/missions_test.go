package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPCreateMissionDiscoveryAndPersistence(t *testing.T) {
	database := t.TempDir()
	ctx, session, stop := hostfixtures.MissionSession(t, database)
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "create_mission" {
			encoded, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &schema); err != nil {
				t.Fatal(err)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("create_mission is unavailable without a controller or registered agents")
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(hostfixtures.MissionDefinition), &want); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		hostfixtures.MissionDefinition,
		`{"name":"minimal","objectives":["Plan future work."],"input_schema":{},"output_schema":{}}`,
		`{"name":"minimal","objectives":["Plan future work."],"required_actions":null,"starting_actions":null,"optional_actions":null,"action_definitions":null,"input_schema":{},"output_schema":{}}`,
	} {
		var value any
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err != nil {
			t.Fatalf("discovered schema rejects a supported mission definition: %v", err)
		}
	}
	for _, body := range []string{
		strings.Replace(hostfixtures.MissionDefinition, `"name": "inspect-domain"`, `"name": "inspect-domain", "status": "ready"`, 1),
		strings.Replace(hostfixtures.MissionDefinition, `"output_schema": {"type":"object","x-note":"\u0061"}`, `"output_schema": {}`, 1),
		strings.Replace(hostfixtures.MissionDefinition, `["PUBLISH_TOKEN"]`, `["PUBLISH_TOKEN","PUBLISH_TOKEN"]`, 1),
	} {
		var value any
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err == nil {
			t.Fatal("discovered schema accepts an unsupported action definition")
		}
	}
	for _, field := range []string{"name", "objectives", "input_schema", "output_schema"} {
		value := make(map[string]any, len(want))
		for key, item := range want {
			value[key] = item
		}
		delete(value, field)
		if err := resolved.Validate(value); err == nil {
			t.Errorf("discovered schema does not require %s", field)
		}
	}
	for _, objectives := range []any{nil, "Plan future work.", []any{}, []any{"Plan future work.", " "}} {
		value := make(map[string]any, len(want))
		for key, item := range want {
			value[key] = item
		}
		value["objectives"] = objectives
		if err := resolved.Validate(value); err == nil {
			t.Errorf("discovered schema accepts invalid objectives: %#v", objectives)
		}
	}
	var actual map[string]any
	encoded := hostfixtures.MissionCall(t, ctx, session, "create_mission", json.RawMessage(hostfixtures.MissionDefinition), &actual)
	for _, lexical := range []string{"9007199254740993", "1.2300e-6", `\u0061`} {
		if !strings.Contains(encoded, lexical) {
			t.Fatalf("action definition schema changed lexical JSON value %q: %s", lexical, encoded)
		}
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("creation changed the mission contract: %+v", actual)
	}
	for _, body := range []string{hostfixtures.MissionDefinition, strings.Replace(hostfixtures.MissionDefinition, "Review actions that have not been implemented yet.", "Overwrite the saved objective.", 1)} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "create_mission", Arguments: json.RawMessage(body)})
		if err != nil || result == nil || !result.IsError || len(result.Content) != 1 {
			t.Fatalf("duplicate creation must return a tool error: result=%+v error=%v", result, err)
		}
		content, ok := result.Content[0].(*sdkmcp.TextContent)
		if !ok || !strings.Contains(content.Text, "mission already exists") {
			t.Fatal("duplicate creation did not identify the existing mission")
		}
	}
	read := func() {
		t.Helper()
		hostfixtures.MissionCall(t, ctx, session, "describe_mission", map[string]any{"mission_name": want["name"]}, &actual)
		if !reflect.DeepEqual(actual, want) {
			t.Fatal("mission lookup lost the contract or duplicate creation changed it")
		}
		var listing struct {
			Missions []map[string]any `json:"missions"`
		}
		hostfixtures.MissionCall(t, ctx, session, "list_missions", map[string]any{}, &listing)
		if len(listing.Missions) != 1 || len(listing.Missions[0]) != 2 || listing.Missions[0]["name"] != want["name"] || !reflect.DeepEqual(listing.Missions[0]["objectives"], want["objectives"]) {
			t.Fatalf("mission listing does not describe the saved definition: %+v", listing)
		}
		var operations struct {
			Operations []json.RawMessage `json:"operations"`
		}
		hostfixtures.MissionCall(t, ctx, session, "list_operations", map[string]any{}, &operations)
		if len(operations.Operations) != 0 {
			t.Fatal("mission creation unexpectedly started an operation")
		}
		for _, tool := range []string{"list_actions", "list_agents"} {
			var catalog map[string][]json.RawMessage
			hostfixtures.MissionCall(t, ctx, session, tool, map[string]any{}, &catalog)
			for _, items := range catalog {
				if len(items) != 0 {
					t.Fatalf("mission action definitions unexpectedly populated %s", tool)
				}
			}
		}
	}
	read()
	session.Close()
	stop()
	ctx, session, _ = hostfixtures.MissionSession(t, database)
	read()
}

func TestMCPCreateMissionRejectsInvalidDefinitions(t *testing.T) {
	database := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"array", `[]`},
		{"empty object", `{}`},
		{"unknown definition field", strings.Replace(hostfixtures.MissionDefinition, `"objectives":`, `"unexpected": true, "objectives":`, 1)},
		{"action readiness field", strings.Replace(hostfixtures.MissionDefinition, `"name": "inspect-domain"`, `"name": "inspect-domain", "status": "ready"`, 1)},
		{"unreferenced action definition", strings.Replace(hostfixtures.MissionDefinition, `"name": "publish-review"`, `"name": "unreferenced-action"`, 1)},
		{"remote action definition reference", strings.Replace(hostfixtures.MissionDefinition, `"output_schema": {"type":"object","x-note":"\u0061"}`, `"output_schema": {"$ref":"https://private.example/schema"}`, 1)},
		{"unknown step field", strings.Replace(hostfixtures.MissionDefinition, `"id": "inspect"`, `"id": "inspect", "unexpected": true`, 1)},
		{"unknown rule field", strings.Replace(hostfixtures.MissionDefinition, `"aggregate-results": {`, `"aggregate-results": {"unexpected": true,`, 1)},
		{"unknown rule parameter", strings.Replace(hostfixtures.MissionDefinition, `"decision_by": "model"`, `"decision_by": "model", "unexpected": true`, 1)},
		{"missing objectives", strings.Replace(hostfixtures.MissionDefinition, `"objectives": ["Review actions that have not been implemented yet.", "Preserve ordered guidance for later execution."],`, "", 1)},
		{"string objectives", strings.Replace(hostfixtures.MissionDefinition, `["Review actions that have not been implemented yet.", "Preserve ordered guidance for later execution."]`, `"String objectives"`, 1)},
		{"invalid starting action", strings.Replace(hostfixtures.MissionDefinition, `"starting_actions": ["inspect-domain"]`, `"starting_actions": ["missing-action"]`, 1)},
		{"nonobject schema", `{"name":"invalid","objectives":["Rejected."],"input_schema":{},"output_schema":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Invalid-params responses can close the SDK's HTTP session.
			ctx, session, _ := hostfixtures.MissionSession(t, database)
			_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "create_mission", Arguments: json.RawMessage(tc.body)})
			var rpcError *jsonrpc.Error
			if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
				t.Fatalf("invalid definition must return JSON-RPC invalid params, got %v", err)
			}
		})
	}
	ctx, session, _ := hostfixtures.MissionSession(t, database)
	var listing struct {
		Missions []json.RawMessage `json:"missions"`
	}
	hostfixtures.MissionCall(t, ctx, session, "list_missions", map[string]any{}, &listing)
	if len(listing.Missions) != 0 {
		t.Fatal("invalid creation changed the mission catalog")
	}
}

func TestMCPCreateMissionStoreFailuresAreSanitizedAndNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		err           error
	}{
		{"duplicate", "mission already exists", missions.ErrMissionExists},
		{"canceled", "mission creation canceled; read describe_mission before retrying", context.Canceled},
		{"timeout", "mission creation timed out; read describe_mission before retrying", context.DeadlineExceeded},
		{"private error", "mission creation failed; read describe_mission before retrying", errors.New("private database credentials and path")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := fakes.NewFailedMissionCreate(tc.err)
			ctx, session := hostfixtures.Session(t, adapter.Config{MissionStore: store})

			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "create_mission", Arguments: json.RawMessage(hostfixtures.MissionDefinition)})

			if err != nil || result == nil || !result.IsError || len(result.Content) != 1 || result.StructuredContent != nil {
				t.Fatalf("store failure must return a text tool error: result=%+v error=%v", result, err)
			}
			content, ok := result.Content[0].(*sdkmcp.TextContent)
			if !ok || content.Text != tc.message {
				t.Fatalf("creation did not return safe failure guidance: %+v", result.Content)
			}
			if attempts, remaining := store.Calls(); attempts != 1 || remaining <= 0 || remaining > 10*time.Second {
				t.Fatalf("creation was retried or lacked a bounded context: attempts=%d remaining=%s", attempts, remaining)
			}
		})
	}
}
