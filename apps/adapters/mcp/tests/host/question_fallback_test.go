package mcp_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	fixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/operations"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPClientsCanUseQuestionFallback(t *testing.T) {
	store := fixtures.QuestionOperations(t)
	future := fixtures.PendingQuestion(time.Now().UTC().Add(time.Hour))
	due := fixtures.PendingQuestion(time.Now().UTC().Add(-time.Minute))
	for _, operation := range []operations.Operation{future, due} {
		if err := store.Create(t.Context(), operation); err != nil {
			t.Fatal(err)
		}
	}
	ctx, session := fixtures.Session(t, adapter.Config{OperationStore: store, Operations: fakes.NewStoredQuestionAnswers(store)})
	before, err := store.Get(ctx, future.ID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "answer_question", Arguments: map[string]any{
		"operation_id": future.ID, "question_id": future.Questions[0].ID, "answer": "Continue",
	}})

	text := questionToolText(t, result, err)
	if !result.IsError || !strings.Contains(text, "question_operator_waiting") ||
		!strings.Contains(text, future.Questions[0].FallbackAt.Format(time.RFC3339Nano)) ||
		!strings.Contains(text, "get_operation") || !strings.Contains(text, "Admin UI") {
		t.Fatalf("early model answer omitted its saved cutoff or operator guidance: %s", text)
	}
	retained, err := store.Get(ctx, future.ID)
	if err != nil || !reflect.DeepEqual(retained, before) {
		t.Fatalf("early answer changed the pending question: %+v, %v", retained, err)
	}

	result, err = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_operation", Arguments: map[string]any{"operation_id": due.ID}})
	var current operations.Operation
	if json.Unmarshal([]byte(questionToolText(t, result, err)), &current) != nil || result.IsError ||
		len(current.Questions) != 1 || !current.Questions[0].Pending() || !current.Questions[0].FallbackAt.Equal(due.Questions[0].FallbackAt) {
		t.Fatal("external MCP client could not read the due question and saved cutoff")
	}
	result, err = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "answer_question", Arguments: map[string]any{
		"operation_id": due.ID, "question_id": current.Questions[0].ID, "answer": "Continue",
	}})
	var answered operations.Operation
	if json.Unmarshal([]byte(questionToolText(t, result, err)), &answered) != nil || result.IsError ||
		len(answered.Questions) != 1 || answered.Questions[0].AnsweredBy != operations.AnswerModel || answered.Questions[0].Answer != "Continue" {
		t.Fatal("external MCP model could not answer the due question through the ordinary tool")
	}
}

func TestMCPQuestionFallbackPreservesOperatorAnswer(t *testing.T) {
	store := fixtures.QuestionOperations(t)
	operation := fixtures.PendingQuestion(time.Now().UTC().Add(-time.Minute))
	if err := store.Create(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	ctx, session := fixtures.Session(t, adapter.Config{OperationStore: store, Operations: fakes.NewStoredQuestionAnswers(store)})
	accepted, err := store.AnswerQuestion(ctx, operation.ID, operation.Questions[0].ID, "Stop", operations.AnswerOperator)
	if err != nil {
		t.Fatal(err)
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "answer_question", Arguments: map[string]any{
		"operation_id": operation.ID, "question_id": operation.Questions[0].ID, "answer": "Continue",
	}})

	text := questionToolText(t, result, err)
	retained, readErr := store.Get(ctx, operation.ID)
	if !result.IsError || !strings.Contains(text, "already answered") || readErr != nil || !reflect.DeepEqual(retained, accepted) {
		t.Fatalf("model fallback replaced an accepted operator answer: %s, %v", text, readErr)
	}
}

func TestMCPQuestionWaitingErrorSurvivesUnreadableState(t *testing.T) {
	store := fixtures.QuestionOperations(t)
	operation := fixtures.PendingQuestion(time.Now().UTC().Add(time.Hour))
	if err := store.Create(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	ctx, session := fixtures.Session(t, adapter.Config{
		OperationStore: fakes.UnreadableQuestions{OperationStore: store}, Operations: fakes.NewStoredQuestionAnswers(store),
	})

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "answer_question", Arguments: map[string]any{
		"operation_id": operation.ID, "question_id": operation.Questions[0].ID, "answer": "Continue",
	}})

	text := questionToolText(t, result, err)
	if !result.IsError || !strings.Contains(text, "question_operator_waiting") || !strings.Contains(text, "get_operation") || strings.Contains(text, "private-question-read-error") {
		t.Fatalf("failed metadata lookup hid or contaminated the original rejection: %s", text)
	}
}

func questionToolText(t *testing.T, result *sdkmcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil || result == nil || len(result.Content) != 1 {
		t.Fatalf("question tool failed: %+v, %v", result, err)
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatal("question tool returned non-text content")
	}
	return content.Text
}
