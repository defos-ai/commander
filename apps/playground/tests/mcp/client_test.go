package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestOperationPreservesQuestionSelectionAndExpiry(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	pastDeadline, expiresAt := now.Add(-time.Minute), now.Add(2*time.Minute)
	latest := map[string]any{"id": "question-latest", "status": "pending", "text": "Choose priority", "options": []string{"Links", "SEO"}, "asked_at": now, "expires_at": expiresAt}
	endpoint := fixture.MCP(t, map[string]fixture.Tool{
		"get_operation": func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{
				"id": "op-1", "status": "waiting", "version": 4,
				"last_question": latest, "remaining_questions": 1,
				"questions": []any{
					map[string]any{"id": "question-expired", "status": "expired", "asked_at": now.Add(-3 * time.Minute), "expires_at": pastDeadline},
					map[string]any{"id": "question-answered", "status": "answered", "answer": "Links", "asked_at": now.Add(-3 * time.Minute), "answered_at": now.Add(-2 * time.Minute)},
					map[string]any{"id": "question-older", "status": "pending", "asked_at": now.Add(-time.Second), "expires_at": expiresAt},
					latest,
				},
			}, nil
		},
	})
	client, err := bridge.Connect(t.Context(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	op, err := client.Operation(t.Context(), "op-1")
	if err != nil || op.LastQuestion == nil || op.LastQuestion.ID != "question-latest" || op.RemainingQuestions != 1 || len(op.Questions) != 4 {
		t.Fatalf("question selection lost: operation=%+v error=%v", op, err)
	}
	if !op.LastQuestion.AskedAt.Equal(now) || !op.LastQuestion.ExpiresAt.Equal(expiresAt) || !op.LastQuestion.Pending(now) || op.LastQuestion.Pending(expiresAt) || !op.Questions[0].ExpiresAt.Equal(pastDeadline) || op.Questions[0].Pending(now) {
		t.Fatalf("question expiry lost: %+v", op.Questions)
	}
	if op.LastQuestion.Status != bridge.QuestionPending || op.Questions[0].Status != bridge.QuestionExpired || op.Questions[1].Status != bridge.QuestionAnswered || op.Questions[1].AnsweredAt == nil || op.Questions[1].Answer != "Links" || op.Questions[1].Pending(now) {
		t.Fatalf("question statuses or answer history lost: %+v", op.Questions)
	}
	for _, status := range []bridge.QuestionStatus{"", bridge.QuestionAnswered, bridge.QuestionExpired} {
		if (bridge.Question{Status: status, ExpiresAt: expiresAt}).Pending(now) {
			t.Fatalf("question with status %q is still pending", status)
		}
	}
}
