package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"commander/apps/playground/internal/chat"
	"commander/apps/playground/internal/llm"
	bridge "commander/apps/playground/internal/mcp"
	"commander/apps/playground/tests/fixture"
)

type modelFunc func(context.Context, []llm.Message, func(string)) (llm.Message, error)

func (f modelFunc) Complete(ctx context.Context, _ string, h []llm.Message, _ []llm.Tool, delta func(string)) (llm.Message, error) {
	return f(ctx, h, delta)
}

func await(t *testing.T, e *chat.Engine, predicate func(chat.Event) bool) chat.Event {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-e.Events():
			if !ok {
				t.Fatal("engine closed")
			}
			if predicate(event) {
				return event
			}
		case <-timer.C:
			t.Fatal("timed out waiting for chat event")
		}
	}
}

func TestChatStaysResponsiveWhileOperationsRun(t *testing.T) {
	// Arrange: a running operation and a model request that we can hold open.
	var version atomic.Int32
	version.Store(1)
	changed := make(chan struct{}, 1)
	endpoint := fixture.MCP(t, map[string]fixture.Tool{
		"get_operation": func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"id": "op-1", "status": "running", "version": version.Load(), "tasks": []any{map[string]string{"id": "task-1", "status": "running", "action_name": "inspect-page"}}}, nil
		},
		"operation_events": func(ctx context.Context, _ json.RawMessage) (any, error) {
			select {
			case <-changed:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return map[string]any{"events": []any{}, "next_sequence": 0}, nil
		},
	})
	commander, err := bridge.Connect(t.Context(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer commander.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	model := modelFunc(func(ctx context.Context, h []llm.Message, delta func(string)) (llm.Message, error) {
		if requests.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return llm.Message{}, ctx.Err()
			}
		}
		text := "Reply to " + h[len(h)-1].Text
		delta(text)
		return llm.Message{Role: "assistant", Text: text}, nil
	})
	e := chat.New(t.Context(), model, commander)
	defer e.Close()
	_ = e.Send("/watch op-1")
	await(t, e, func(event chat.Event) bool { return event.Kind == "operation" })

	// Act: ask a question, queue another, and receive operation progress meanwhile.
	_ = e.Send("first")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("model did not start")
	}
	_ = e.Send("second")
	_ = e.Send("/status")
	await(t, e, func(event chat.Event) bool {
		return event.Kind == "notice" && strings.Contains(event.Text, "queued messages")
	})
	version.Store(2)
	changed <- struct{}{}
	await(t, e, func(event chat.Event) bool { return event.Kind == "operation" && event.Operation.Version == 2 })
	close(release)
	await(t, e, func(event chat.Event) bool { return event.Kind == "delta" && event.Text == "Reply to second" })

	// Assert: background progress needed no model turn, and queued chat was delivered.
	if requests.Load() != 2 {
		t.Fatalf("unexpected model requests: %d", requests.Load())
	}
}

func TestOperatorAnswersCannotBeInventedByModel(t *testing.T) {
	// Arrange: the MCP server advertises answer_question, but the bridge restricts it.
	var answers atomic.Int32
	endpoint := fixture.MCP(t, map[string]fixture.Tool{
		"answer_question": func(context.Context, json.RawMessage) (any, error) { answers.Add(1); return map[string]any{}, nil },
	})
	commander, err := bridge.Connect(t.Context(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer commander.Close()
	var calls atomic.Int32
	model := modelFunc(func(_ context.Context, h []llm.Message, delta func(string)) (llm.Message, error) {
		if calls.Add(1) == 1 {
			return llm.Message{Role: "assistant", Calls: []llm.Call{{ID: "call-1", Name: "answer_question", Arguments: json.RawMessage(`{"answer":"invented"}`)}}}, nil
		}
		if h[len(h)-1].Role != "tool" || !strings.Contains(h[len(h)-1].Text, "not available") {
			t.Error("missing restricted-tool result")
		}
		delta("Waiting for your answer.")
		return llm.Message{Role: "assistant", Text: "Waiting"}, nil
	})
	e := chat.New(t.Context(), model, commander)
	defer e.Close()
	// Act.
	_ = e.Send("continue")
	await(t, e, func(event chat.Event) bool { return event.Kind == "idle" })
	// Assert.
	if answers.Load() != 0 {
		t.Fatal("model submitted an operator answer")
	}
}

func TestContinuationReviewIsDeduplicatedAndOperatorAnswerIsExact(t *testing.T) {
	// Arrange: a pending model decision that becomes an operator question.
	var mu sync.Mutex
	status, decision := "pending", "model"
	version := 1
	answered := ""
	changed := make(chan struct{}, 8)
	snapshot := func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		questions := []any{}
		if decision == "user" || answered != "" {
			q := map[string]any{"id": "question-1", "text": "Which priority?", "options": []string{"Accessibility", "Broken links"}}
			if answered != "" {
				q["answered_at"] = "2026-09-21T12:00:00Z"
			}
			questions = append(questions, q)
		}
		return map[string]any{"id": "op-1", "status": "running", "version": version, "tasks": []any{map[string]any{"id": "task-1", "status": "succeeded", "continuation": map[string]any{"version": version, "status": status, "decision_by": decision}}}, "questions": questions}
	}
	endpoint := fixture.MCP(t, map[string]fixture.Tool{
		"get_operation": func(context.Context, json.RawMessage) (any, error) { return snapshot(), nil },
		"operation_events": func(ctx context.Context, _ json.RawMessage) (any, error) {
			select {
			case <-changed:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return map[string]any{"next_sequence": 0}, nil
		},
		"ask_question": func(context.Context, json.RawMessage) (any, error) {
			mu.Lock()
			decision = "user"
			status = "waiting"
			version++
			mu.Unlock()
			changed <- struct{}{}
			return map[string]string{"id": "question-1"}, nil
		},
		"answer_question": func(_ context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Answer string }
			_ = json.Unmarshal(raw, &args)
			mu.Lock()
			answered = args.Answer
			decision = "model"
			status = "resolved"
			version++
			mu.Unlock()
			changed <- struct{}{}
			return snapshot(), nil
		},
	})
	commander, err := bridge.Connect(t.Context(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer commander.Close()
	var reviews atomic.Int32
	model := modelFunc(func(_ context.Context, h []llm.Message, delta func(string)) (llm.Message, error) {
		if strings.HasPrefix(h[len(h)-1].Text, "Background operation update") {
			reviews.Add(1)
			return llm.Message{Role: "assistant", Calls: []llm.Call{{ID: "question-call", Name: "ask_question", Arguments: json.RawMessage(`{"operation_id":"op-1","text":"Which priority?"}`)}}}, nil
		}
		delta("Waiting for guidance.")
		return llm.Message{Role: "assistant", Text: "Waiting"}, nil
	})
	e := chat.New(t.Context(), model, commander)
	defer e.Close()

	// Act: repeat the same operation update, then answer using the displayed option.
	_ = e.Send("/watch op-1")
	await(t, e, func(event chat.Event) bool { return event.Kind == "question" })
	changed <- struct{}{}
	_ = e.Send("/answer question-1 2")
	await(t, e, func(event chat.Event) bool {
		return event.Kind == "success" && strings.Contains(event.Text, "answer_question")
	})

	// Assert: the repeated snapshot did not repeat the model decision.
	mu.Lock()
	defer mu.Unlock()
	if reviews.Load() != 1 || answered != "Broken links" {
		t.Fatalf("reviews=%d answer=%q", reviews.Load(), answered)
	}
}

func TestQueuedContinuationChecksCurrentStateBeforeCallingModel(t *testing.T) {
	for _, change := range []string{"resolved", "new version", "operator decision", "terminal"} {
		t.Run(change, func(t *testing.T) {
			// Arrange: a continuation queues behind a foreground model request.
			var updated atomic.Bool
			endpoint := fixture.MCP(t, map[string]fixture.Tool{
				"get_operation": func(context.Context, json.RawMessage) (any, error) {
					status, decision, operationStatus, version := "pending", "model", "running", 1
					if updated.Load() {
						switch change {
						case "resolved":
							status = "resolved"
						case "new version":
							version = 2
						case "operator decision":
							decision = "user"
						case "terminal":
							operationStatus = "cancelled"
						}
					}
					return map[string]any{"id": "op-1", "status": operationStatus, "version": version, "tasks": []any{
						map[string]any{"id": "task-1", "status": "succeeded", "continuation": map[string]any{"version": version, "status": status, "decision_by": decision}},
					}}, nil
				},
				"operation_events": func(ctx context.Context, _ json.RawMessage) (any, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				},
			})
			commander, err := bridge.Connect(t.Context(), endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer commander.Close()
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			model := modelFunc(func(ctx context.Context, _ []llm.Message, _ func(string)) (llm.Message, error) {
				if calls.Add(1) == 1 {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return llm.Message{}, ctx.Err()
					}
				}
				return llm.Message{Role: "assistant", Text: "Ready."}, nil
			})
			e := chat.New(t.Context(), model, commander)
			defer e.Close()
			_ = e.Send("hello")
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("foreground model request did not start")
			}
			_ = e.Send("/watch op-1")
			await(t, e, func(event chat.Event) bool { return event.Kind == "operation" })

			// Act: the saved continuation changes before its queued review begins.
			updated.Store(true)
			close(release)
			for range 2 {
				await(t, e, func(event chat.Event) bool { return event.Kind == "idle" })
			}

			// Assert: stale work never prompts the model to make a decision.
			if calls.Load() != 1 {
				t.Fatalf("model calls=%d; queued review used obsolete state", calls.Load())
			}
		})
	}
}
