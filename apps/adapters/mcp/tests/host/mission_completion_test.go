package mcp_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPMissionCompletionPolicyRoundTrip(t *testing.T) {
	ctx, session := hostfixtures.MissionCatalogSession(t, t.TempDir())
	var created map[string]any
	hostfixtures.MissionCall(t, ctx, session, "create_mission",
		json.RawMessage(hostfixtures.MissionWithCompletion(`{"decision_by":"operator"}`)), &created)

	if !reflect.DeepEqual(created["completion"], map[string]any{"decision_by": "operator"}) {
		t.Fatal("mission creation did not retain its completion setting")
	}
	for _, policy := range []string{`{"decision_by":"model"}`, `null`, `{"decision_by":"operator"}`, ""} {
		var updated map[string]any
		body := hostfixtures.MissionDefinition
		if policy != "" {
			body = hostfixtures.MissionWithCompletion(policy)
		}

		hostfixtures.MissionCall(t, ctx, session, "update_mission", json.RawMessage(body), &updated)
		var saved map[string]any
		hostfixtures.MissionCall(t, ctx, session, "describe_mission", map[string]any{"mission_name": created["name"]}, &saved)

		if !reflect.DeepEqual(saved, updated) {
			t.Fatal("mission readback changed its completion setting")
		}
		var expected any
		if policy != "" {
			if err := json.Unmarshal([]byte(policy), &expected); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(saved["completion"], expected) {
			t.Fatalf("completion readback differs for %s", policy)
		}
		if expected == nil {
			if _, exists := saved["completion"]; exists {
				t.Fatal("an absent completion setting was serialized as present")
			}
		}
	}
}

func TestMCPMissionCompletionSchemaAndValidation(t *testing.T) {
	ctx, session := hostfixtures.MissionCatalogSession(t, t.TempDir())
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tool := range tools.Tools {
		if tool.Name != "create_mission" && tool.Name != "update_mission" {
			continue
		}
		found++
		t.Run(tool.Name, func(t *testing.T) {
			encoded, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var schema jsonschema.Schema
			if err := json.Unmarshal(encoded, &schema); err != nil {
				t.Fatal(err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, policy := range []string{"", `null`, `{"decision_by":"model"}`, `{"decision_by":"operator"}`} {
				body := hostfixtures.MissionDefinition
				if policy != "" {
					body = hostfixtures.MissionWithCompletion(policy)
				}
				var input any
				if err := json.Unmarshal([]byte(body), &input); err != nil {
					t.Fatal(err)
				}

				if err := resolved.Validate(input); err != nil {
					t.Fatalf("discovered schema rejected completion %q: %v", policy, err)
				}
			}
			for _, policy := range []string{`{}`, `{"decision_by":""}`, `{"decision_by":"human"}`, `{"decision_by":false}`, `{"decision_by":"operator","fallback_at":"later"}`, `[]`} {
				t.Run(policy, func(t *testing.T) {
					body := hostfixtures.MissionWithCompletion(policy)
					var input any
					if err := json.Unmarshal([]byte(body), &input); err != nil {
						t.Fatal(err)
					}
					callContext, invalidSession := hostfixtures.MissionCatalogSession(t, t.TempDir())

					_, err := invalidSession.CallTool(callContext, &sdkmcp.CallToolParams{Name: tool.Name, Arguments: json.RawMessage(body)})

					if err := resolved.Validate(input); err == nil {
						t.Fatal("discovered schema accepted invalid completion settings")
					}
					var rpcError *jsonrpc.Error
					if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
						t.Fatalf("invalid completion setting must return invalid params: %v", err)
					}
				})
			}
		})
	}
	if found != 2 {
		t.Fatal("mission creation and update tools must advertise the completion setting")
	}
}
