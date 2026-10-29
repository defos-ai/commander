package mcp_test

import (
	"errors"
	"testing"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPRemovedMissionStepsAreRejected(t *testing.T) {
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{"create_mission", map[string]any{"name": "review", "objectives": []string{"Review"}, "input_schema": map[string]any{}, "output_schema": map[string]any{}, "steps": []any{}}},
		{"assign_task", map[string]any{"operation_id": "10000000-0000-4000-8000-000000000001", "action_name": "inspect", "input": map[string]any{}, "step_id": "review"}},
		{"ask_question", map[string]any{"operation_id": "10000000-0000-4000-8000-000000000001", "text": "Continue?", "step_id": "review"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, session := hostfixtures.Session(t, adapter.Config{})

			_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: test.name, Arguments: test.args})

			var rpcError *jsonrpc.Error
			if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
				t.Fatalf("removed mission step field must return invalid params, got %v", err)
			}
		})
	}
}
