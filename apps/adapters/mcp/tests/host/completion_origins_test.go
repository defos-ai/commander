package mcp_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	fixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPCompletionOriginIsAssignedByAdapter(t *testing.T) {
	const operationID = "00000000-0000-4000-8000-000000000001"
	commands := fakes.NewCompletionCommands(nil)
	ctx, session := fixtures.Session(t, adapter.Config{Operations: commands})
	for _, origin := range []string{"operator", "model"} {
		_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "complete_operation", Arguments: map[string]any{
			"operation_id": operationID, "output": map[string]any{"summary": "Reviewed"}, "completed_by": origin,
		}})

		var invalid *jsonrpc.Error
		if !errors.As(err, &invalid) || invalid.Code != jsonrpc.CodeInvalidParams {
			t.Fatalf("client-supplied completion origin was not invalid params: %v", err)
		}
		select {
		case <-commands.Submitted():
			t.Fatal("client-supplied completion origin reached the controller")
		default:
		}
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "complete_operation", Arguments: map[string]any{
		"operation_id": operationID, "output": map[string]any{"summary": "Reviewed"},
	}})

	text := completionToolText(t, result, err)
	if result.IsError {
		t.Fatalf("MCP completion failed: %s", text)
	}
	select {
	case command := <-commands.Submitted():
		if command.OperationID != operationID || string(command.Output) != `{"summary":"Reviewed"}` || command.CompletedBy != operations.CompletionModel {
			t.Fatal("MCP completion lost its target, output or adapter-assigned origin")
		}
	default:
		t.Fatal("MCP completion did not reach the controller")
	}
	var saved operations.Operation
	if json.Unmarshal([]byte(text), &saved) != nil || saved.Status != operations.Completed || saved.CompletedBy != operations.CompletionModel {
		t.Fatal("MCP response omitted the saved completion origin")
	}
}

func TestMCPCompletionRequiresOperatorHandoff(t *testing.T) {
	const operationID = "00000000-0000-4000-8000-000000000001"
	for _, scenario := range []struct {
		name      string
		failure   error
		uncertain bool
	}{
		{name: "operator required", failure: operations.ErrCompletionOperatorRequired},
		{name: "uncertain write", failure: errors.Join(operations.ErrCompletionOperatorRequired, operations.ErrWriteUncertain), uncertain: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			commands := fakes.NewCompletionCommands(scenario.failure)
			ctx, session := fixtures.Session(t, adapter.Config{Operations: commands})

			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "complete_operation", Arguments: map[string]any{
				"operation_id": operationID, "output": map[string]any{"summary": "Ready for review"},
			}})

			text := completionToolText(t, result, err)
			if !result.IsError {
				t.Fatal("rejected completion was reported as successful")
			}
			if scenario.uncertain {
				if text != scenario.failure.Error() {
					t.Fatal("operator guidance masked an uncertain completion outcome")
				}
				return
			}
			if !strings.Contains(text, "completion_operator_required") || !strings.Contains(text, "authenticated HTTP API") ||
				!strings.Contains(text, "question fallback does not change this policy") {
				t.Fatalf("operator-only completion omitted its handoff guidance: %s", text)
			}
		})
	}
}

func completionToolText(t *testing.T, result *sdkmcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil || result == nil || len(result.Content) != 1 {
		t.Fatalf("completion tool failed: %+v, %v", result, err)
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatal("completion tool returned non-text content")
	}
	return content.Text
}
