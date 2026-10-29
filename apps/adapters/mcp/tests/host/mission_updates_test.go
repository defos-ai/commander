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
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPUpdateMissionDiscoveryAndPersistence(t *testing.T) {
	database := t.TempDir()
	ctx, session := hostfixtures.MissionCatalogSession(t, database)
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var createSchema, updateSchema any
	for _, tool := range tools.Tools {
		switch tool.Name {
		case "create_mission":
			createSchema = tool.InputSchema
		case "update_mission":
			updateSchema = tool.InputSchema
		}
	}
	if createSchema == nil || updateSchema == nil || !reflect.DeepEqual(createSchema, updateSchema) {
		t.Fatal("mission creation and editing must expose the same complete definition schema")
	}
	var initial map[string]any
	hostfixtures.MissionCall(t, ctx, session, "create_mission", json.RawMessage(hostfixtures.MissionDefinition), &initial)
	body := strings.ReplaceAll(hostfixtures.MissionDefinition, "inspect-domain", "inspect-updated-domain")
	body = strings.Replace(body, "Review actions that have not been implemented yet.", "Investigate the updated objectives.", 1)
	body = strings.Replace(body, "Inspect a requested domain.", "Inspect the domain and retain detailed evidence.", 1)
	body = strings.Replace(body, `"required": ["summary"]`, `"required": ["summary"], "x-edited": true`, 1)

	var updated map[string]any
	encoded := hostfixtures.MissionCall(t, ctx, session, "update_mission", json.RawMessage(body), &updated)

	var expected map[string]any
	if err := json.Unmarshal([]byte(body), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated, expected) {
		t.Fatal("editing did not preserve the updated objectives, actions, rules and schemas")
	}
	for _, lexical := range []string{"9007199254740993", "1.2300e-6", `\u0061`, `"x-edited":true`} {
		if !strings.Contains(encoded, lexical) {
			t.Fatalf("editing changed schema JSON %q", lexical)
		}
	}
	session.Close()
	ctx, session = hostfixtures.MissionCatalogSession(t, database)
	var saved map[string]any
	hostfixtures.MissionCall(t, ctx, session, "describe_mission", map[string]any{"mission_name": expected["name"]}, &saved)
	if !reflect.DeepEqual(saved, updated) {
		t.Fatal("updated mission did not survive reopening the store and MCP session")
	}
}

func TestMCPUpdateMissionDoesNotCreateOrRename(t *testing.T) {
	ctx, session := hostfixtures.MissionCatalogSession(t, t.TempDir())
	var initial map[string]any
	hostfixtures.MissionCall(t, ctx, session, "create_mission", json.RawMessage(hostfixtures.MissionDefinition), &initial)
	body := strings.Replace(hostfixtures.MissionDefinition, `"Research / #1?"`, `"Renamed mission"`, 1)

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "update_mission", Arguments: json.RawMessage(body)})

	assertMissionToolError(t, result, err, missions.ErrUnknownMission.Error())
	var saved map[string]any
	hostfixtures.MissionCall(t, ctx, session, "describe_mission", map[string]any{"mission_name": initial["name"]}, &saved)
	if !reflect.DeepEqual(saved, initial) {
		t.Fatal("changing the name modified the original mission")
	}
	var listing struct {
		Missions []json.RawMessage `json:"missions"`
	}
	hostfixtures.MissionCall(t, ctx, session, "list_missions", map[string]any{}, &listing)
	if len(listing.Missions) != 1 {
		t.Fatal("updating an unknown name created another mission")
	}
}

func TestMCPUpdateMissionRejectsInvalidDefinitions(t *testing.T) {
	for _, scenario := range []struct{ name, body string }{
		{"missing objectives", `{"name":"research","input_schema":{},"output_schema":{}}`},
		{"invalid action membership", strings.Replace(hostfixtures.MissionDefinition, `"starting_actions": ["inspect-domain"]`, `"starting_actions": ["missing-action"]`, 1)},
		{"unknown field", strings.Replace(hostfixtures.MissionDefinition, `"objectives":`, `"mission_name":"another", "objectives":`, 1)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, session := hostfixtures.MissionCatalogSession(t, t.TempDir())

			_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "update_mission", Arguments: json.RawMessage(scenario.body)})

			var rpcError *jsonrpc.Error
			if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
				t.Fatalf("invalid mission update must return JSON-RPC invalid params: %v", err)
			}
		})
	}
}

func TestMCPUpdateMissionStoreFailuresAreSanitizedAndNeverRetried(t *testing.T) {
	for _, scenario := range []struct {
		name, message string
		err           error
	}{
		{"missing", missions.ErrUnknownMission.Error(), missions.ErrUnknownMission},
		{"canceled", "mission update canceled; read describe_mission before retrying", context.Canceled},
		{"timeout", "mission update timed out; read describe_mission before retrying", context.DeadlineExceeded},
		{"private error", "mission update failed; read describe_mission before retrying", errors.New("private database credentials and path")},
	} {
		for _, stage := range []string{"read", "write"} {
			t.Run(scenario.name+"/"+stage, func(t *testing.T) {
				base := hostfixtures.MissionCatalogStore(t, t.TempDir())
				mission, err := missions.LoadMission([]byte(hostfixtures.MissionDefinition))
				if err != nil {
					t.Fatal(err)
				}
				if err := base.Create(t.Context(), mission); err != nil {
					t.Fatal(err)
				}
				var readErr, writeErr error
				var expectedWrites int32
				if stage == "read" {
					readErr = scenario.err
				} else {
					writeErr, expectedWrites = scenario.err, 1
				}
				store := fakes.NewFailedMissionUpdate(base, readErr, writeErr)
				ctx, session := hostfixtures.Session(t, adapter.Config{MissionStore: store})

				result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "update_mission", Arguments: json.RawMessage(hostfixtures.MissionDefinition)})

				assertMissionToolError(t, result, err, scenario.message)
				reads, writes, remaining := store.Calls()
				if reads != 1 || writes != expectedWrites || remaining <= 0 || remaining > 10*time.Second {
					t.Fatalf("update retried or lost its bounded context: reads=%d writes=%d remaining=%s", reads, writes, remaining)
				}
			})
		}
	}
}

func assertMissionToolError(t *testing.T, result *sdkmcp.CallToolResult, err error, expected string) {
	t.Helper()
	if err != nil || result == nil || !result.IsError || len(result.Content) != 1 || result.StructuredContent != nil {
		t.Fatalf("mission failure must return one text tool error: result=%+v error=%v", result, err)
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok || content.Text != expected {
		t.Fatalf("mission failure guidance is not the expected safe message: %+v", result.Content)
	}
}
