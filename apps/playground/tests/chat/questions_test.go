package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestTypedOperatorAnswerRoutesExactInputAndReportsOutcome(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprintf("rejected=%t", reject), func(t *testing.T) {
			var answered atomic.Bool
			var calls, reviews atomic.Int32
			changed := make(chan struct{}, 8)
			const answer = "  2\nwith a note  "
			snapshot := func() map[string]any {
				q := map[string]any{"id": "question-1", "status": bridge.QuestionPending, "text": "Which priority?", "options": []string{"Links", "SEO"}, "asked_at": time.Now().Add(-time.Minute), "expires_at": time.Now().Add(time.Minute)}
				var latest any = q
				version, decision, status := 1, "user", "waiting"
				if answered.Load() {
					q["status"] = bridge.QuestionAnswered
					q["answered_at"], q["answer"] = time.Now(), answer
					latest = nil
					version, decision, status = 2, "model", "pending"
				}
				return map[string]any{"id": "op-1", "status": "running", "version": version, "questions": []any{q}, "last_question": latest, "remaining_questions": 0,
					"tasks": []any{map[string]any{"id": "task-1", "status": "succeeded", "continuation": map[string]any{"version": version, "decision_by": decision, "status": status}}}}
			}
			endpoint := fixture.MCP(t, map[string]fixture.Tool{
				"get_operation": func(context.Context, json.RawMessage) (any, error) { return snapshot(), nil },
				"operation_events": func(ctx context.Context, _ json.RawMessage) (any, error) {
					select {
					case <-changed:
						return map[string]any{"next_sequence": 0}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
				"answer_question": func(_ context.Context, raw json.RawMessage) (any, error) {
					calls.Add(1)
					var args struct {
						OperationID string `json:"operation_id"`
						QuestionID  string `json:"question_id"`
						Answer      string
					}
					if err := json.Unmarshal(raw, &args); err != nil || args.OperationID != "op-1" || args.QuestionID != "question-1" || args.Answer != answer {
						t.Errorf("typed operator input changed: %s error=%v", raw, err)
					}
					if reject {
						return nil, errors.New("question expired before the answer arrived")
					}
					answered.Store(true)
					changed <- struct{}{}
					return snapshot(), nil
				},
			})
			commander, err := bridge.Connect(t.Context(), endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer commander.Close()
			reviewed := make(chan struct{}, 8)
			model := modelFunc(func(_ context.Context, history []llm.Message, _ func(string)) (llm.Message, error) {
				reviews.Add(1)
				text := history[len(history)-1].Text
				if reject || !strings.HasPrefix(text, "The operator answered") || !strings.Contains(text, `2\nwith a note`) {
					t.Errorf("unexpected answer review: %s", text)
				}
				reviewed <- struct{}{}
				return llm.Message{Role: "assistant", Text: "Answer reviewed."}, nil
			})
			e := chat.New(t.Context(), model, commander)
			defer e.Close()
			_ = e.Send("/watch op-1")
			await(t, e, func(event chat.Event) bool { return event.Kind == "operation" && event.Operation.LastQuestion != nil })
			if err := e.AnswerQuestion("op-1", "question-1", " \n"); err == nil {
				t.Fatal("blank typed answer accepted")
			}
			if err := e.AnswerQuestion("op-1", "question-1", answer); err != nil {
				t.Fatal(err)
			}
			start := await(t, e, func(event chat.Event) bool { return event.Kind == "answer_start" })
			end := await(t, e, func(event chat.Event) bool { return event.Kind == "answer_end" })
			if start.OperationID != "op-1" || start.QuestionID != "question-1" || end.OperationID != "op-1" || end.QuestionID != "question-1" || (end.Text != "") != reject || calls.Load() != 1 {
				t.Fatalf("answer result lost: start=%+v end=%+v calls=%d", start, end, calls.Load())
			}
			if !reject && end.Answer != answer || reject && end.Answer != "" {
				t.Fatalf("accepted answer transcript changed: %+v", end)
			}
			if !reject {
				select {
				case <-reviewed:
				case <-time.After(3 * time.Second):
					t.Fatal("accepted answer did not resume review")
				}
				// Both the direct refresh and watcher see the same answer and task
				// continuation; they must schedule just one model turn.
				changed <- struct{}{}
				select {
				case <-reviewed:
					t.Fatal("answer and continuation scheduled duplicate reviews")
				case <-time.After(350 * time.Millisecond):
				}
			} else if reviews.Load() != 0 {
				t.Fatal("rejected answer was presented to the model as accepted")
			}
		})
	}
}

func TestExpiredQuestionsResumeReviewWithoutInventingAnswers(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(fmt.Sprintf("linked-task=%t", linked), func(t *testing.T) {
			var mu sync.Mutex
			version, expired := 1, false
			changed := make(chan struct{}, 8)
			var answers, reviews atomic.Int32
			deadline := time.Now().Add(-time.Second)
			snapshot := func() map[string]any {
				mu.Lock()
				defer mu.Unlock()
				q := map[string]any{"id": "question-1", "status": bridge.QuestionPending, "text": "Allow a wider review?", "options": []string{"Yes", "No"}, "asked_at": deadline.Add(-2 * time.Minute), "expires_at": deadline}
				decision, continuationStatus, status := "user", "waiting", "waiting"
				var latest any = q
				if expired {
					q["status"] = bridge.QuestionExpired
					latest = nil
					decision, continuationStatus, status = "model", "pending", "planning"
				}
				var tasks []any
				if linked {
					tasks = append(tasks, map[string]any{"id": "task-1", "status": "succeeded", "continuation": map[string]any{"version": 2, "status": continuationStatus, "decision_by": decision}})
				}
				return map[string]any{"id": "op-1", "status": status, "version": version, "questions": []any{q}, "last_question": latest, "tasks": tasks}
			}
			endpoint := fixture.MCP(t, map[string]fixture.Tool{
				"get_operation": func(context.Context, json.RawMessage) (any, error) { return snapshot(), nil },
				"operation_events": func(ctx context.Context, _ json.RawMessage) (any, error) {
					select {
					case <-changed:
						return map[string]any{"next_sequence": 0}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
				"answer_question": func(context.Context, json.RawMessage) (any, error) {
					answers.Add(1)
					return nil, errors.New("expired questions cannot be answered")
				},
			})
			commander, err := bridge.Connect(t.Context(), endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer commander.Close()
			reviewed := make(chan string, 8)
			model := modelFunc(func(_ context.Context, history []llm.Message, _ func(string)) (llm.Message, error) {
				reviews.Add(1)
				reviewed <- history[len(history)-1].Text
				return llm.Message{Role: "assistant", Text: "Continue within the original scope."}, nil
			})
			e := chat.New(t.Context(), model, commander)
			defer e.Close()
			_ = e.Send("/watch op-1")
			await(t, e, func(event chat.Event) bool { return event.Kind == "operation" && event.Operation.Version == 1 })
			mu.Lock()
			version, expired = 2, true
			mu.Unlock()
			changed <- struct{}{}
			await(t, e, func(event chat.Event) bool { return event.Kind == "operation" && event.Operation.Version == 2 })
			select {
			case prompt := <-reviewed:
				if !strings.Contains(prompt, "No response was received") || !strings.Contains(prompt, "Expiry is not consent") || strings.Contains(prompt, "The operator answered") {
					t.Fatalf("expiry was misrepresented: %s", prompt)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("question expiry did not resume model review")
			}
			if err := e.AnswerQuestion("op-1", "question-1", "Yes"); err != nil {
				t.Fatal(err)
			}
			end := await(t, e, func(event chat.Event) bool { return event.Kind == "answer_end" })
			if end.Text == "" || answers.Load() != 0 {
				t.Fatalf("expired answer reached MCP: event=%+v calls=%d", end, answers.Load())
			}
			mu.Lock()
			version++
			mu.Unlock()
			changed <- struct{}{}
			await(t, e, func(event chat.Event) bool { return event.Kind == "operation" && event.Operation.Version == 3 })
			select {
			case <-reviewed:
				t.Fatal("unchanged expired question triggered another review")
			case <-time.After(100 * time.Millisecond):
			}
			if reviews.Load() != 1 || answers.Load() != 0 {
				t.Fatalf("reviews=%d fabricated answers=%d", reviews.Load(), answers.Load())
			}
		})
	}
}

func TestAcceptedAnswerIsReportedBeforeOperationRefresh(t *testing.T) {
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var reads atomic.Int32
	endpoint := fixture.MCP(t, map[string]fixture.Tool{
		"get_operation": func(ctx context.Context, _ json.RawMessage) (any, error) {
			if reads.Add(1) > 1 {
				close(refreshStarted)
				select {
				case <-releaseRefresh:
					return nil, errors.New("fixture refresh released")
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			q := map[string]any{"id": "question-1", "status": bridge.QuestionPending, "text": "Which priority?", "expires_at": time.Now().Add(time.Minute)}
			return map[string]any{"id": "op-1", "status": "waiting", "version": 1, "questions": []any{q}, "last_question": q}, nil
		},
		"operation_events": func(ctx context.Context, _ json.RawMessage) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		"answer_question": func(context.Context, json.RawMessage) (any, error) {
			return map[string]string{"id": "op-1"}, nil
		},
	})
	commander, err := bridge.Connect(t.Context(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer commander.Close()
	model := modelFunc(func(context.Context, []llm.Message, func(string)) (llm.Message, error) {
		t.Error("review must wait for refreshed operation state")
		return llm.Message{}, errors.New("unexpected model call")
	})
	e := chat.New(t.Context(), model, commander)
	defer e.Close()
	defer close(releaseRefresh)
	_ = e.Send("/watch op-1")
	await(t, e, func(event chat.Event) bool { return event.Kind == "operation" })
	if err := e.AnswerQuestion("op-1", "question-1", "SEO"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("operation refresh did not start")
	}
	// The refresh stays blocked until test cleanup; acceptance must already
	// be available to the selector and transcript independently of that read.
	end := await(t, e, func(event chat.Event) bool { return event.Kind == "answer_end" })
	if end.Text != "" || end.Answer != "SEO" || end.OperationID != "op-1" || end.QuestionID != "question-1" {
		t.Fatalf("accepted answer was delayed or changed: %+v", end)
	}
}
