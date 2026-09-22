package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"commander/apps/playground/internal/chat"
	"commander/apps/playground/internal/llm"
	bridge "commander/apps/playground/internal/mcp"
	"commander/apps/playground/tests/fixture"
)

func TestToolFailureExplainsErrorToOperatorAndModel(t *testing.T) {
	// Arrange: a rejected assignment, returned through the real MCP client.
	const reason = "action input: url is required"
	endpoint := fixture.MCP(t, map[string]fixture.Tool{
		"assign_task": func(context.Context, json.RawMessage) (any, error) {
			return nil, errors.New(reason)
		},
	})
	commander, err := bridge.Connect(t.Context(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer commander.Close()
	var calls atomic.Int32
	model := modelFunc(func(_ context.Context, history []llm.Message, _ func(string)) (llm.Message, error) {
		if calls.Add(1) == 1 {
			return llm.Message{Role: "assistant", Calls: []llm.Call{{ID: "call-1", Name: "assign_task", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		last := history[len(history)-1]
		if last.Role != "tool" || !strings.Contains(last.Text, reason) {
			t.Errorf("model did not receive tool error: %+v", last)
		}
		return llm.Message{Role: "assistant", Text: "The assignment needs a URL."}, nil
	})
	e := chat.New(t.Context(), model, commander)
	defer e.Close()

	// Act.
	if err := e.Send("Review the website"); err != nil {
		t.Fatal(err)
	}
	event := await(t, e, func(event chat.Event) bool { return event.Kind == "error" })
	await(t, e, func(event chat.Event) bool { return event.Kind == "idle" })

	// Assert: the operator sees the cause and the model can continue the conversation.
	if !strings.Contains(event.Text, "assign_task failed:") || !strings.Contains(event.Text, reason) {
		t.Fatalf("missing failure details: %s", event.Text)
	}
	if calls.Load() != 2 {
		t.Fatalf("model calls=%d, want 2", calls.Load())
	}
}

func TestModelErrorsKeepChatUsableWithoutOperatorHandoff(t *testing.T) {
	for _, background := range []bool{false, true} {
		name := "chat"
		if background {
			name = "continuation review"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange: hold the first model call while the user queues another message.
			var asks, calls atomic.Int32
			endpoint := fixture.MCP(t, map[string]fixture.Tool{
				"get_operation": func(context.Context, json.RawMessage) (any, error) {
					return map[string]any{"id": "op-1", "status": "planning", "version": 1, "tasks": []any{
						map[string]any{"id": "task-1", "action_name": "inspect-page", "status": "succeeded", "continuation": map[string]any{"version": 1, "status": "pending", "decision_by": "model"}},
					}}, nil
				},
				"operation_events": func(ctx context.Context, _ json.RawMessage) (any, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				},
				"ask_question": func(context.Context, json.RawMessage) (any, error) {
					asks.Add(1)
					return map[string]string{"id": "unexpected-question"}, nil
				},
			})
			commander, err := bridge.Connect(t.Context(), endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer commander.Close()
			started, release := make(chan struct{}), make(chan struct{})
			model := modelFunc(func(ctx context.Context, history []llm.Message, delta func(string)) (llm.Message, error) {
				if calls.Add(1) == 1 {
					close(started)
					select {
					case <-ctx.Done():
						return llm.Message{}, ctx.Err()
					case <-release:
						return llm.Message{}, errors.New("model API returned HTTP 400; rerun with --debug for details")
					}
				}
				text := "Reply to " + history[len(history)-1].Text
				delta(text)
				return llm.Message{Role: "assistant", Text: text}, nil
			})
			e := chat.New(t.Context(), model, commander)
			defer e.Close()
			first := "Review the website"
			if background {
				first = "/watch op-1"
			}
			_ = e.Send(first)
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("model did not start")
			}
			_ = e.Send("queued message")
			await(t, e, func(event chat.Event) bool {
				return event.Kind == "notice" && strings.Contains(event.Text, "Message queued")
			})

			// Act: fail the active request, then continue chatting after the queued reply.
			close(release)
			errorCount, questionCount := 0, 0
			observe := func(event chat.Event) {
				if event.Kind == "error" {
					errorCount++
					if !strings.Contains(event.Text, "HTTP 400") || !strings.Contains(event.Text, "--debug") {
						t.Errorf("unexpected error: %s", event.Text)
					}
				}
				if event.Kind == "question" {
					questionCount++
				}
			}
			await(t, e, func(event chat.Event) bool {
				observe(event)
				return event.Kind == "delta" && event.Text == "Reply to queued message"
			})
			_ = e.Send("another message")
			await(t, e, func(event chat.Event) bool {
				observe(event)
				return event.Kind == "delta" && event.Text == "Reply to another message"
			})
			await(t, e, func(event chat.Event) bool { observe(event); return event.Kind == "idle" })

			// Assert: one ordinary error, no forced question or repeated failed request.
			if errorCount != 1 || questionCount != 0 || asks.Load() != 0 || calls.Load() != 3 {
				t.Fatalf("errors=%d questions=%d ask calls=%d model calls=%d", errorCount, questionCount, asks.Load(), calls.Load())
			}
		})
	}
}
