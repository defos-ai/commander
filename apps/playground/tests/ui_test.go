package playground_test

import (
	"context"
	"encoding/json"
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
	tea "github.com/charmbracelet/bubbletea"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProviderSelector(t *testing.T) {
	model := ui.New(t.Context(), ui.Config{NoColor: true}, io.Discard)
	defer model.Close()
	model.Update(tea.WindowSizeMsg{Width: 90, Height: 30})

	model.Update(tea.KeyMsg{Type: tea.KeyDown})
	selected := model.View()
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(selected, "DEFOS | Commander Playground") || !strings.Contains(selected, "› Anthropic") || !strings.Contains(model.View(), "Anthropic API key") {
		t.Fatalf("unexpected selector: %s\n%s", selected, model.View())
	}
	if strings.Contains(selected, "\x1b[") {
		t.Fatal("no-color rendering contains ANSI colors")
	}
}

func TestQuestionSelector(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	op := bridge.Operation{ID: "operation-1", Status: "waiting", Version: 1, Questions: []bridge.Question{
		{ID: "older", Status: bridge.QuestionPending, Text: "Older question", AskedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Minute)},
		{ID: "latest", Status: bridge.QuestionPending, Text: "Which release priority?", Options: []string{"Accessibility", "Broken links"}, AskedAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Minute)},
	}}
	answers := make(chan string, 1)
	server := mcp.NewServer(&mcp.Implementation{Name: "question-fixture", Version: "1"}, nil)
	for _, name := range []string{"get_operation", "operation_events", "answer_question"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			mu.Lock()
			defer mu.Unlock()
			var payload any = op
			switch req.Params.Name {
			case "operation_events":
				payload = map[string]any{"events": []any{}, "next_sequence": 0}
			case "answer_question":
				var args struct {
					Answer      string
					OperationID string `json:"operation_id"`
					QuestionID  string `json:"question_id"`
				}
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return nil, err
				}
				if args.OperationID != op.ID || args.QuestionID != "latest" {
					t.Errorf("answer targeted the wrong question: %+v", args)
				}
				answers <- args.Answer
				op.Questions[1].Status = bridge.QuestionAnswered
				op.Questions[1].Answer = args.Answer
				op.Questions[0].ExpiresAt = time.Now().Add(500 * time.Millisecond)
				op.Version++
				payload = op
			}
			encoded, _ := json.Marshal(payload)
			return &mcp.CallToolResult{StructuredContent: json.RawMessage(encoded)}, nil
		})
	}
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer endpoint.Close()
	u := newUI(t, ctx, endpoint.URL, "http://127.0.0.1:1")
	defer u.model.Close()
	model, key, text, wait := u.model, u.key, u.text, u.wait

	text("/watch operation-1")
	key(tea.KeyMsg{Type: tea.KeyEnter})
	text("unsent chat draft")
	wait("latest question", func(view string) bool { return strings.Contains(view, "Which release priority?") })
	view := model.View()
	if !strings.Contains(view, "1 other question(s) waiting") || !strings.Contains(view, "left") {
		t.Fatalf("missing queue count or countdown: %s", view)
	}
	key(tea.KeyMsg{Type: tea.KeyTab})
	if !strings.Contains(model.View(), "unsent chat draft") {
		t.Fatal("switching to chat lost the draft")
	}
	key(tea.KeyMsg{Type: tea.KeyTab})
	key(tea.KeyMsg{Type: tea.KeyDown})
	key(tea.KeyMsg{Type: tea.KeyEnter})
	wait("recorded answer", func(view string) bool { return strings.Contains(view, "Operator answer: Broken links") })

	select {
	case answer := <-answers:
		if answer != "Broken links" {
			t.Fatalf("submitted %q", answer)
		}
	default:
		t.Fatal("answer was not submitted")
	}
	wait("expiry", func(view string) bool { return !strings.Contains(view, "Operator input") })
	if !strings.Contains(model.View(), "unsent chat draft") {
		t.Fatal("answering or expiry lost the chat draft")
	}
}

func TestChatRecoversAfterModelFailureOrCancellation(t *testing.T) {
	for _, scenario := range []string{"HTTP error", "truncated tool stream", "Ctrl+C"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var calls, requests atomic.Int32
			server := mcp.NewServer(&mcp.Implementation{Name: "error-fixture", Version: "1"}, nil)
			server.AddTool(&mcp.Tool{Name: "start_operation", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				return &mcp.CallToolResult{StructuredContent: map[string]any{}}, nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
			defer endpoint.Close()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					switch scenario {
					case "HTTP error":
						http.Error(w, `{"error":{"message":"Invalid fixture model"}}`, http.StatusBadRequest)
					case "truncated tool stream":
						writeModelDelta(w, toolDelta("start_operation", map[string]any{}), nil)
					case "Ctrl+C":
						writeModelDelta(w, map[string]any{"content": "Waiting for model"}, nil)
						w.(http.Flusher).Flush()
						select {
						case <-r.Context().Done():
						case <-ctx.Done():
						}
					}
					return
				}
				writeModelDelta(w, map[string]any{"content": "**Chat recovered**"}, "stop")
			}))
			defer provider.Close()
			u := newUI(t, ctx, endpoint.URL, provider.URL)
			// Close the model before HTTP servers, including on failed assertions.
			defer u.model.Close()

			u.text("Start the review")
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			want := "HTTP 400"
			if scenario == "truncated tool stream" {
				want = "model stream ended before completion"
			} else if scenario == "Ctrl+C" {
				u.wait("active stream", func(view string) bool { return strings.Contains(view, "Waiting for model") })
				u.key(tea.KeyMsg{Type: tea.KeyCtrlC})
				want = "Reply stopped"
			}
			u.wait("error and idle state", func(view string) bool {
				return strings.Contains(view, want) && !strings.Contains(view, "Thinking")
			})

			if calls.Load() != 0 {
				t.Fatal("an unfinished model response executed a tool")
			}
			u.text("Continue chatting")
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			u.wait("recovered reply", func(view string) bool {
				return strings.Contains(view, "Chat recovered") && !strings.Contains(view, "Thinking")
			})
			if requests.Load() != 2 || strings.Contains(u.model.View(), "**Chat recovered**") {
				t.Fatalf("chat recovery or markdown rendering failed: %s", u.model.View())
			}
		})
	}
}

func writeModelDelta(w http.ResponseWriter, delta map[string]any, finish any) {
	w.Header().Set("Content-Type", "text/event-stream")
	encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\n", encoded)
	if finish != nil {
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

// Drive Bubble Tea's public update loop, including async commands, and inspect
// its current rendered frame instead of relying on stale terminal output.
type uiDriver struct {
	t        *testing.T
	ctx      context.Context
	model    *ui.Model
	messages chan tea.Msg
}

func newUI(t *testing.T, ctx context.Context, endpoint, provider string) *uiDriver {
	t.Helper()
	u := &uiDriver{t: t, ctx: ctx, messages: make(chan tea.Msg, 32), model: ui.New(ctx, ui.Config{Provider: "other", Model: "fixture", BaseURL: provider, MCPURL: endpoint, Keys: map[string]string{"other": "test-only"}, NoColor: true}, io.Discard)}
	t.Cleanup(u.model.Close)
	u.model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	u.run(u.model.Init())
	u.wait("connection", func(view string) bool { return strings.Contains(view, "Commander connected") })
	return u
}

func (u *uiDriver) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, child := range batch {
				u.run(child)
			}
			return
		}
		select {
		case u.messages <- msg:
		case <-u.ctx.Done():
		}
	}()
}

func (u *uiDriver) key(key tea.KeyMsg) { _, cmd := u.model.Update(key); u.run(cmd) }
func (u *uiDriver) text(text string)   { u.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)}) }
func (u *uiDriver) wait(description string, check func(string) bool) {
	u.t.Helper()
	for !check(u.model.View()) {
		select {
		case msg := <-u.messages:
			_, cmd := u.model.Update(msg)
			u.run(cmd)
		case <-u.ctx.Done():
			u.t.Fatalf("waiting for %s: %s", description, u.model.View())
		}
	}
}
