package ui_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bridge "commander/apps/playground/internal/mcp"
	"commander/apps/playground/internal/ui"
	"commander/apps/playground/tests/fixture"
	tea "github.com/charmbracelet/bubbletea"
)

type submittedAnswer struct {
	OperationID string `json:"operation_id"`
	QuestionID  string `json:"question_id"`
	Answer      string `json:"answer"`
}

type questionUI struct {
	model      *ui.Model
	next       tea.Cmd
	mu         sync.Mutex
	operations map[string]bridge.Operation
	changed    map[string]chan struct{}
	answers    chan submittedAnswer
}

func newQuestionUI(t *testing.T, answerError func() error) *questionUI {
	t.Helper()
	f := &questionUI{operations: map[string]bridge.Operation{}, changed: map[string]chan struct{}{}, answers: make(chan submittedAnswer, 8)}
	endpoint := fixture.MCP(t, map[string]fixture.Tool{
		"get_operation": func(_ context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			_ = json.Unmarshal(raw, &input)
			f.mu.Lock()
			defer f.mu.Unlock()
			// Copy the full wire snapshot while holding the fixture state lock.
			data, err := json.Marshal(f.operations[input.OperationID])
			return json.RawMessage(data), err
		},
		"operation_events": func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			_ = json.Unmarshal(raw, &input)
			f.mu.Lock()
			changed := f.changed[input.OperationID]
			f.mu.Unlock()
			select {
			case <-changed:
				return map[string]any{"next_sequence": 0}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-t.Context().Done():
				return nil, t.Context().Err()
			}
		},
		"answer_question": func(_ context.Context, raw json.RawMessage) (any, error) {
			var input submittedAnswer
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			f.answers <- input
			if answerError != nil {
				if err := answerError(); err != nil {
					return nil, err
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			op := f.operations[input.OperationID]
			answered := time.Now().UTC().Format(time.RFC3339Nano)
			for i := range op.Questions {
				if op.Questions[i].ID == input.QuestionID {
					op.Questions[i].Status = bridge.QuestionAnswered
					op.Questions[i].AnsweredAt = &answered
					op.Questions[i].Answer = input.Answer
				}
			}
			if op.LastQuestion != nil && op.LastQuestion.ID == input.QuestionID {
				op.LastQuestion = nil
			}
			op.Version++
			f.operations[op.ID] = op
			data, err := json.Marshal(op)
			return json.RawMessage(data), err
		},
	})
	modelAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.SSE(w, map[string]any{"content": "Ordinary chat is still available."}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(modelAPI.Close)
	f.model = ui.New(t.Context(), ui.Config{BaseURL: modelAPI.URL, Model: "fixture", MCPURL: endpoint, Keys: map[string]string{"other": "fixture"}, NoColor: true}, io.Discard)
	t.Cleanup(f.model.Close)
	f.model.Update(tea.WindowSizeMsg{Width: 120, Height: 45})
	key(f.model, tea.KeyEnd)
	connect := key(f.model, tea.KeyEnter)
	_, f.next = f.model.Update(connect())
	return f
}

func (f *questionUI) publish(operation bridge.Operation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.operations[operation.ID] = operation
	if f.changed[operation.ID] == nil {
		f.changed[operation.ID] = make(chan struct{}, 1)
	}
	select {
	case f.changed[operation.ID] <- struct{}{}:
	default:
	}
}

func (f *questionUI) until(t *testing.T, predicate func(string) bool) {
	t.Helper()
	for !predicate(f.model.View()) {
		if f.next == nil {
			t.Fatal("missing event subscription")
		}
		messages := make(chan tea.Msg, 1)
		go func(cmd tea.Cmd) { messages <- cmd() }(f.next)
		_, f.next = f.model.Update(awaitMessage(t, messages))
	}
}

func (f *questionUI) watch(t *testing.T, id, question string) {
	t.Helper()
	typeText(f.model, "/watch "+id)
	f.until(t, func(view string) bool { return strings.Contains(view, question) })
}

func questionOperation(id string, questions ...bridge.Question) bridge.Operation {
	return bridge.Operation{ID: id, Version: 1, Status: "waiting", Questions: questions}
}

func TestQuestionSelectorSubmitsChoiceAndRetriesFailure(t *testing.T) {
	// Arrange: a pending question with four options; saving its first answer fails.
	var attempts atomic.Int32
	f := newQuestionUI(t, func() error {
		if attempts.Add(1) == 1 {
			return errors.New("answer storage unavailable")
		}
		return nil
	})
	q := bridge.Question{Status: bridge.QuestionPending, ID: "hidden-question-id", Text: "Which release priority?", Options: []string{"Accessibility", "Broken links", "Performance", "Content"}, AskedAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Minute)}
	f.publish(questionOperation("operation-choice", q))
	f.watch(t, "operation-choice", q.Text)
	view := f.model.View()
	for _, expected := range []string{"› Accessibility", "Write an answer…", "Tab to chat", " left"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("missing %q: %s", expected, view)
		}
	}
	if strings.Contains(view, q.ID) || strings.Contains(view, "/answer ") {
		t.Fatalf("raw question command leaked into panel: %s", view)
	}

	// Act: choose with arrows and retry the same answer after a visible failure.
	key(f.model, tea.KeyDown)
	key(f.model, tea.KeyEnter)
	if !strings.Contains(f.model.View(), "Sending answer…") {
		t.Fatal("submission progress missing")
	}
	f.until(t, func(view string) bool { return strings.Contains(view, "Answer failed: answer storage unavailable") })
	key(f.model, tea.KeyEnter)
	f.until(t, func(view string) bool { return !strings.Contains(view, "✷ Operator input") })
	f.until(t, func(view string) bool { return strings.Contains(view, "Operator answer: Broken links") })

	// Assert: both submissions target the exact question and selected option.
	for range 2 {
		select {
		case answer := <-f.answers:
			if answer.OperationID != "operation-choice" || answer.QuestionID != q.ID || answer.Answer != "Broken links" {
				t.Fatalf("answer=%+v", answer)
			}
		default:
			t.Fatal("missing answer submission")
		}
	}
}

func TestQuestionFreeTextPreservesChatDraftAndAllowsChat(t *testing.T) {
	// Arrange: an operation is watched while the operator has an unsent chat draft.
	f := newQuestionUI(t, nil)
	f.publish(questionOperation("operation-draft"))
	f.watch(t, "operation-draft", "0/0 tasks finished")
	f.model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("my unsent chat draft")})
	q := bridge.Question{Status: bridge.QuestionPending, ID: "question-free", Text: "What should we focus on?", Options: []string{"Links", "Layout"}, AskedAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Minute)}
	op := questionOperation("operation-draft", q)
	op.Version = 2
	f.publish(op)
	f.until(t, func(view string) bool { return strings.Contains(view, q.Text) })

	// Act: write an answer, switch to normal chat, and return to submit the draft.
	f.model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Focus on keyboard navigation")})
	key(f.model, tea.KeyTab)
	if !strings.Contains(f.model.View(), "my unsent chat draft") {
		t.Fatal("question stole the chat draft")
	}
	key(f.model, tea.KeyEnter)
	f.until(t, func(view string) bool { return strings.Contains(view, "Ordinary chat is still available.") })
	key(f.model, tea.KeyTab)
	if !strings.Contains(f.model.View(), "Focus on keyboard navigation") {
		t.Fatal("chat replaced the answer draft")
	}
	key(f.model, tea.KeyEnter)
	f.until(t, func(view string) bool { return !strings.Contains(view, "✷ Operator input") })
	f.until(t, func(view string) bool { return strings.Contains(view, "Operator answer: Focus on keyboard navigation") })

	// Assert: answering uses the typed operator API, while ordinary chat stays usable.
	select {
	case answer := <-f.answers:
		if answer.Answer != "Focus on keyboard navigation" {
			t.Fatalf("answer=%+v", answer)
		}
	default:
		t.Fatal("free-text answer was not submitted")
	}
	if !strings.Contains(f.model.View(), "my unsent chat draft") {
		t.Fatal("ordinary chat was lost")
	}
}

func TestQuestionPanelSelectsLatestAndIgnoresStaleOrClosedQuestions(t *testing.T) {
	// Arrange: three pending questions across operations, plus already closed history.
	f := newQuestionUI(t, nil)
	now := time.Now()
	old := bridge.Question{Status: bridge.QuestionPending, ID: "old", Text: "Older operation question", AskedAt: now.Add(-20 * time.Second), ExpiresAt: now.Add(time.Minute)}
	other := bridge.Question{Status: bridge.QuestionPending, ID: "other", Text: "Oldest pending question", AskedAt: now.Add(-30 * time.Second), ExpiresAt: now.Add(time.Minute)}
	latest := bridge.Question{Status: bridge.QuestionPending, ID: "latest", Text: "Latest operation question", Options: []string{"Proceed"}, AskedAt: now, ExpiresAt: now.Add(time.Minute)}
	answeredTime := now.Format(time.RFC3339Nano)
	closed := bridge.Question{Status: bridge.QuestionAnswered, ID: "closed", Text: "Already answered", AskedAt: now.Add(time.Second), AnsweredAt: &answeredTime}
	a := questionOperation("aaaaaaaa-operation", old, closed)
	b := questionOperation("bbbbbbbb-operation", other, latest)
	b.LastQuestion, b.RemainingQuestions = &latest, 1
	f.publish(a)
	f.publish(b)
	f.watch(t, a.ID, old.Text)
	f.watch(t, b.ID, latest.Text)
	view := f.model.View()
	if !strings.Contains(view, "operation bbbbbbbb") || !strings.Contains(view, "2 other question(s) waiting") || strings.Contains(view, old.Text) || strings.Contains(view, closed.Text) {
		t.Fatalf("wrong latest question/count: %s", view)
	}

	// Act: expire the newest, deliver an older snapshot, then end another operation.
	expired := latest
	expired.Status = bridge.QuestionExpired
	updated := questionOperation(b.ID, other, expired)
	updated.Version = 2
	f.publish(updated)
	f.until(t, func(view string) bool { return strings.Contains(view, old.Text) })
	f.publish(b)
	// Wait for a watcher refresh marker by reading through /status; the later
	// terminal update also proves the UI keeps the highest operation versions.
	key(f.model, tea.KeyTab)
	typeText(f.model, "/status")
	f.until(t, func(view string) bool { return strings.Contains(view, "watching 2 operations") })
	a.Status, a.Version = "cancelled", 2
	f.publish(a)
	f.until(t, func(view string) bool { return strings.Contains(view, other.Text) })

	// Assert: no stale, terminal, or answered question reappears.
	view = f.model.View()
	if strings.Contains(view, latest.Text) || strings.Contains(view, old.Text) || strings.Contains(view, closed.Text) || strings.Contains(view, "other question(s) waiting") {
		t.Fatalf("closed question was rendered again: %s", view)
	}
}

func TestQuestionDeadlineDismissesPanelWithoutServerUpdate(t *testing.T) {
	// Arrange: a near-deadline question displays its remaining time.
	f := newQuestionUI(t, nil)
	q := bridge.Question{Status: bridge.QuestionPending, ID: "expires-locally", Text: "Time-limited operator question", Options: []string{"Continue"}, AskedAt: time.Now(), ExpiresAt: time.Now().Add(time.Second)}
	f.publish(questionOperation("operation-expiry", q))
	f.watch(t, "operation-expiry", q.Text)
	if !strings.Contains(f.model.View(), "0:01 left") {
		t.Fatalf("countdown missing: %s", f.model.View())
	}

	// Act: deadline passes without a newer operation snapshot.
	<-time.After(time.Until(q.ExpiresAt) + 10*time.Millisecond)
	key(f.model, tea.KeyEnter)

	// Assert: the expired panel disappears and Enter cannot submit its stale choice.
	if strings.Contains(f.model.View(), q.Text) || strings.Contains(f.model.View(), "✷ Operator input") {
		t.Fatalf("expired question remains: %s", f.model.View())
	}
	select {
	case answer := <-f.answers:
		t.Fatalf("expired answer submitted: %+v", answer)
	default:
	}
}
