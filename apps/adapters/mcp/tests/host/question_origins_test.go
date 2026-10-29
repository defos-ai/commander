package mcp_test

import (
	"encoding/json"
	"errors"
	"testing"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPQuestionAnswerOriginIsAssignedByAdapter(t *testing.T) {
	const operationID = "00000000-0000-4000-8000-000000000001"
	const questionID = "00000000-0000-4000-8000-000000000002"
	commands := fakes.NewQuestionAnswers()
	ctx, session := hostfixtures.Session(t, adapter.Config{Operations: commands})

	_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "answer_question", Arguments: map[string]any{
		"operation_id": operationID, "question_id": questionID, "answer": "Proceed", "answered_by": "operator",
	}})

	var invalid *jsonrpc.Error
	if !errors.As(err, &invalid) || invalid.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("client-supplied answer origin was not invalid params: %v", err)
	}
	select {
	case <-commands.Submitted():
		t.Fatal("client-supplied answer origin reached the controller")
	default:
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "answer_question", Arguments: map[string]any{
		"operation_id": operationID, "question_id": questionID, "answer": "Proceed",
	}})

	if err != nil || result == nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("MCP answer failed: result=%+v error=%v", result, err)
	}
	select {
	case command := <-commands.Submitted():
		if command.OperationID != operationID || command.QuestionID != questionID || command.Answer != "Proceed" || command.AnsweredBy != operations.AnswerModel {
			t.Fatal("MCP answer lost its target, value or adapter-assigned origin")
		}
	default:
		t.Fatal("MCP answer did not reach the controller")
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	var saved operations.Operation
	if !ok || json.Unmarshal([]byte(content.Text), &saved) != nil || len(saved.Questions) != 1 || saved.Questions[0].AnsweredBy != operations.AnswerModel {
		t.Fatal("MCP response omitted the saved answer origin")
	}
}
