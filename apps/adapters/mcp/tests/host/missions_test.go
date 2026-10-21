package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/host"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const missionDefinition = `{
  "name": "Research / #1?",
  "objectives": ["Review actions that have not been implemented yet.", "Preserve ordered guidance for later execution."],
  "required_actions": ["inspect-domain", "summarize-domain"],
  "optional_actions": ["publish-review"],
  "starting_actions": ["inspect-domain"],
  "steps": [{
    "id": "inspect",
    "objective": "Inspect the requested domain.",
    "agent_types": ["future-agent"],
    "required_actions": ["inspect-domain"]
  }],
  "rules": {
    "aggregate-results": {"for_actions": ["inspect-domain"]},
    "decide-next-actions": {
      "for_actions": ["summarize-domain"],
      "params": {"decision_by": "model"}
    },
    "same-agent": {"for_actions": ["inspect-domain", "summarize-domain"]}
  },
  "input_schema": {
    "type": "object",
    "properties": {"domain": {"$ref": "#/$defs/domain"}},
    "$defs": {"domain": {"type": "string", "minLength": 1}},
    "required": ["domain"],
    "additionalProperties": false,
    "x-purpose": "Schema extensions belong to the mission author."
  },
  "output_schema": {
    "type": "object",
    "properties": {"summary": {"type": "string"}},
    "required": ["summary"]
  }
}`

func TestMCPCreateMissionDiscoveryAndPersistence(t *testing.T) {
	database := t.TempDir()
	ctx, session, stop := missionSession(t, database)
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
	if err := json.Unmarshal([]byte(missionDefinition), &want); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		missionDefinition,
		`{"name":"minimal","objectives":["Plan future work."],"input_schema":{},"output_schema":{}}`,
		`{"name":"minimal","objectives":["Plan future work."],"required_actions":null,"starting_actions":null,"optional_actions":null,"input_schema":{},"output_schema":{}}`,
	} {
		var value any
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err != nil {
			t.Fatalf("discovered schema rejects a supported mission definition: %v", err)
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
	missionCall(t, ctx, session, "create_mission", json.RawMessage(missionDefinition), &actual)
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("creation changed the mission contract: %+v", actual)
	}
	for _, body := range []string{missionDefinition, strings.Replace(missionDefinition, "Review actions that have not been implemented yet.", "Overwrite the saved objective.", 1)} {
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
		missionCall(t, ctx, session, "describe_mission", map[string]any{"mission_name": want["name"]}, &actual)
		if !reflect.DeepEqual(actual, want) {
			t.Fatal("mission lookup lost the contract or duplicate creation changed it")
		}
		var listing struct {
			Missions []map[string]any `json:"missions"`
		}
		missionCall(t, ctx, session, "list_missions", map[string]any{}, &listing)
		if len(listing.Missions) != 1 || len(listing.Missions[0]) != 2 || listing.Missions[0]["name"] != want["name"] || !reflect.DeepEqual(listing.Missions[0]["objectives"], want["objectives"]) {
			t.Fatalf("mission listing does not describe the saved definition: %+v", listing)
		}
		var operations struct {
			Operations []json.RawMessage `json:"operations"`
		}
		missionCall(t, ctx, session, "list_operations", map[string]any{}, &operations)
		if len(operations.Operations) != 0 {
			t.Fatal("mission creation unexpectedly started an operation")
		}
	}
	read()
	session.Close()
	stop()
	ctx, session, _ = missionSession(t, database)
	read()
}

func TestMCPCreateMissionRejectsInvalidDefinitions(t *testing.T) {
	database := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"array", `[]`},
		{"empty object", `{}`},
		{"unknown definition field", strings.Replace(missionDefinition, `"objectives":`, `"unexpected": true, "objectives":`, 1)},
		{"unknown step field", strings.Replace(missionDefinition, `"id": "inspect"`, `"id": "inspect", "unexpected": true`, 1)},
		{"unknown rule field", strings.Replace(missionDefinition, `"aggregate-results": {`, `"aggregate-results": {"unexpected": true,`, 1)},
		{"unknown rule parameter", strings.Replace(missionDefinition, `"decision_by": "model"`, `"decision_by": "model", "unexpected": true`, 1)},
		{"blank objective item", strings.Replace(missionDefinition, "Review actions that have not been implemented yet.", " ", 1)},
		{"legacy objective", strings.Replace(missionDefinition, `"objectives": ["Review actions that have not been implemented yet.", "Preserve ordered guidance for later execution."]`, `"objective": "Legacy single objective"`, 1)},
		{"missing objectives", strings.Replace(missionDefinition, `"objectives": ["Review actions that have not been implemented yet.", "Preserve ordered guidance for later execution."],`, "", 1)},
		{"string objectives", strings.Replace(missionDefinition, `["Review actions that have not been implemented yet.", "Preserve ordered guidance for later execution."]`, `"String objectives"`, 1)},
		{"null objectives", strings.Replace(missionDefinition, `["Review actions that have not been implemented yet.", "Preserve ordered guidance for later execution."]`, `null`, 1)},
		{"empty objectives", strings.Replace(missionDefinition, `["Review actions that have not been implemented yet.", "Preserve ordered guidance for later execution."]`, `[]`, 1)},
		{"dot name", strings.Replace(missionDefinition, `"name": "Research / #1?"`, `"name": "."`, 1)},
		{"duplicate action", strings.Replace(missionDefinition, `"inspect-domain", "summarize-domain"`, `"inspect-domain", "inspect-domain"`, 1)},
		{"invalid starting action", strings.Replace(missionDefinition, `"starting_actions": ["inspect-domain"]`, `"starting_actions": ["missing-action"]`, 1)},
		{"invalid rule", strings.Replace(missionDefinition, `"decision_by": "model"`, `"decision_by": "operator"`, 1)},
		{"invalid schema type", strings.Replace(missionDefinition, `"type": "string", "minLength": 1`, `"type": "not-a-schema-type"`, 1)},
		{"nonobject schema", `{"name":"invalid","objectives":["Rejected."],"input_schema":{},"output_schema":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Invalid-params responses can close the SDK's HTTP session.
			ctx, session, _ := missionSession(t, database)
			_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "create_mission", Arguments: json.RawMessage(tc.body)})
			var rpcError *jsonrpc.Error
			if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
				t.Fatalf("invalid definition must return JSON-RPC invalid params, got %v", err)
			}
		})
	}
	ctx, session, _ := missionSession(t, database)
	var listing struct {
		Missions []json.RawMessage `json:"missions"`
	}
	missionCall(t, ctx, session, "list_missions", map[string]any{}, &listing)
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
			store := &failedMissionCreate{err: tc.err}
			ctx, session := mcpSession(t, adapter.Config{MissionStore: store})
			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "create_mission", Arguments: json.RawMessage(missionDefinition)})
			if err != nil || result == nil || !result.IsError || len(result.Content) != 1 || result.StructuredContent != nil {
				t.Fatalf("store failure must return a text tool error: result=%+v error=%v", result, err)
			}
			content, ok := result.Content[0].(*sdkmcp.TextContent)
			if !ok || content.Text != tc.message {
				t.Fatalf("creation did not return safe failure guidance: %+v", result.Content)
			}
			if attempts, remaining := store.attempts.Load(), time.Duration(store.remaining.Load()); attempts != 1 || remaining <= 0 || remaining > 10*time.Second {
				t.Fatalf("creation was retried or lacked a bounded context: attempts=%d remaining=%s", attempts, remaining)
			}
		})
	}
}

type failedMissionCreate struct {
	missions.MissionStore
	err       error
	attempts  atomic.Int32
	remaining atomic.Int64
}

func (store *failedMissionCreate) Create(ctx context.Context, _ missions.Mission) error {
	store.attempts.Add(1)
	deadline, _ := ctx.Deadline()
	store.remaining.Store(int64(time.Until(deadline)))
	return store.err
}

func missionSession(t *testing.T, database string) (context.Context, *sdkmcp.ClientSession, func()) {
	t.Helper()
	const token = "mission-creation-test-token"
	endpoint, client, stop := startMCPWithOptions(t, []string{"--database-url=" + database},
		cli.Options{Credentials: &host.BearerCredentials{Token: token}}, io.Discard)
	client.Transport = bearerTransport{token: token, next: client.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "mission-test", Version: "1"}, nil).Connect(ctx,
		&sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return ctx, session, stop
}

func missionCall(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, tool string, arguments, output any) {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.StructuredContent != nil {
		t.Fatalf("%s must return one JSON text block without structured content: %+v", tool, result)
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("%s did not return JSON text", tool)
	}
	if err := json.Unmarshal([]byte(content.Text), output); err != nil {
		t.Fatal(err)
	}
}
