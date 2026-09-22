package playground_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	bridge "commander/apps/playground/internal/mcp"
	"commander/apps/playground/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProviderSelector(t *testing.T) {
	// Arrange.
	model := ui.New(t.Context(), ui.Config{NoColor: true}, io.Discard)
	defer model.Close()
	model.Update(tea.WindowSizeMsg{Width: 90, Height: 30})

	// Act: choose the second provider using the keyboard.
	model.Update(tea.KeyMsg{Type: tea.KeyDown})
	selected := model.View()
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert: setup uses a selector and the selected provider's key prompt.
	if !strings.Contains(selected, "DEFOS | Commander Playground") || !strings.Contains(selected, "› Anthropic") || !strings.Contains(model.View(), "Anthropic API key") {
		t.Fatalf("unexpected selector: %s\n%s", selected, model.View())
	}
	if strings.Contains(selected, "\x1b[") {
		t.Fatal("no-color rendering contains ANSI colors")
	}
}

func TestQuestionSelector(t *testing.T) {
	// Arrange: the UI receives operation snapshots through its public MCP client.
	// This fixture supplies only questions, not a simulated Commander workflow.
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
	model := ui.New(ctx, ui.Config{Provider: "other", Model: "fixture", BaseURL: "http://127.0.0.1:1", MCPURL: endpoint.URL, Keys: map[string]string{"other": "test-only"}, NoColor: true}, io.Discard)
	defer model.Close()
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	messages := make(chan tea.Msg, 32)
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		go func() {
			msg := cmd()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, child := range batch {
					run(child)
				}
				return
			}
			select {
			case messages <- msg:
			case <-ctx.Done():
			}
		}()
	}
	key := func(key tea.KeyMsg) { _, cmd := model.Update(key); run(cmd) }
	text := func(value string) { key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}) }
	wait := func(description string, check func(string) bool) {
		t.Helper()
		for !check(model.View()) {
			select {
			case msg := <-messages:
				_, cmd := model.Update(msg)
				run(cmd)
			case <-ctx.Done():
				t.Fatalf("waiting for %s: %s", description, model.View())
			}
		}
	}
	run(model.Init())
	wait("connection", func(view string) bool { return strings.Contains(view, "Commander connected") })

	// Act: watch questions while retaining a normal chat draft, then select an
	// option. Tab must switch focus without submitting or losing the draft.
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

	// Assert: only the selected answer was submitted; answered/expired panels
	// disappear and ordinary chat remains intact.
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
