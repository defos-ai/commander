package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	bridge "commander/apps/playground/internal/mcp"
	"commander/apps/playground/tests/fixture"
)

func TestMCPDiscoveryCallsAndArtifacts(t *testing.T) {
	// Arrange: a real Streamable HTTP MCP server with tool and resource endpoints.
	endpoint := fixture.MCP(t, map[string]fixture.Tool{
		"list_missions": func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"missions": []any{map[string]string{"name": "review"}}}, nil
		},
		"answer_question":  func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("operator-only") },
		"operation_events": func(context.Context, json.RawMessage) (any, error) { return nil, nil },
	})
	client, err := bridge.Connect(t.Context(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Act: discover functions, call MCP, and read referenced evidence.
	tools := client.Tools()
	result, err := client.Call(t.Context(), "list_missions", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := client.Call(t.Context(), "read_artifact", json.RawMessage(`{"uri":"missions://artifacts/fixture"}`))
	_, badErr := client.Call(t.Context(), "read_artifact", json.RawMessage(`{"uri":"file:///etc/passwd"}`))

	// Assert: model functions exclude operator answers and internal long polling.
	for _, tool := range tools {
		if tool.Name == "answer_question" || tool.Name == "operation_events" {
			t.Fatalf("exposed internal tool: %s", tool.Name)
		}
	}
	if len(tools) != 2 || !strings.Contains(result.Text, "review") || err != nil || !strings.Contains(artifact.Text, "saved evidence") || badErr == nil {
		t.Fatalf("tools=%+v result=%+v artifact=%+v err=%v", tools, result, artifact, err)
	}
	bounded := bridge.Bounded([]byte(strings.Repeat("a", 200000)))
	if !json.Valid([]byte(bounded)) || !strings.Contains(bounded, `"truncated":true`) {
		t.Fatal("large evidence lost its truncation marker")
	}
}
