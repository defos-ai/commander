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

	tea "github.com/charmbracelet/bubbletea"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
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

func TestLocalAPICredentialsDuringSetup(t *testing.T) {
	for _, local := range []*ui.LocalAPI{nil, {URL: "http://127.0.0.1:4274", Username: "commander", Password: "local-only-fixture-password"}} {
		model := ui.New(t.Context(), ui.Config{NoColor: true, LocalAPI: local}, io.Discard)
		defer model.Close()
		model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/help")})
		model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if local != nil && (!strings.Contains(model.View(), "/credentials") || strings.Contains(model.View(), local.Password)) {
			t.Fatal("local help must advertise credential lookup without revealing the password")
		}
		model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/credentials")})
		model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		view := model.View()
		if !strings.Contains(view, "Select your model provider:") {
			t.Fatal("credential lookup changed the setup stage")
		}
		if local == nil {
			if !strings.Contains(view, "only with --all-in-one") {
				t.Fatal("remote session did not explain unavailable local credentials")
			}
		} else if !strings.Contains(view, "API: "+local.URL) || !strings.Contains(view, "Username: "+local.Username) || !strings.Contains(view, "Password: "+local.Password) {
			t.Fatal("explicit credential lookup did not display the local API credentials")
		}
	}
}

func TestLocalAPICredentialsStayOutOfModelRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	local := &ui.LocalAPI{URL: "http://127.0.0.1:4274", Username: "local-api-user", Password: "local-only-fixture-password"}
	var requests, calls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "credentials-fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "list_missions", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer endpoint.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("could not inspect model request")
		}
		for _, private := range []string{local.Password, local.Username, local.URL, "/credentials"} {
			if strings.Contains(string(body), private) {
				t.Error("local credential lookup leaked into a model request")
			}
		}
		writeModelDelta(w, map[string]any{"content": "Ready to chat"}, "stop")
	}))
	defer provider.Close()
	u := newUIWithConfig(t, ctx, ui.Config{Provider: "other", Model: "fixture", BaseURL: provider.URL, MCPURL: endpoint.URL, Keys: map[string]string{"other": "test-only"}, NoColor: true, LocalAPI: local})
	defer u.model.Close()
	if strings.Contains(u.model.View(), local.Password) {
		t.Fatal("local password was displayed before explicit lookup")
	}
	u.text("/credentials")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(u.model.View(), "Password: "+local.Password) {
		t.Fatal("explicit credential lookup did not display the local password")
	}
	u.text("Hello")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("normal chat after credential lookup", func(view string) bool {
		return strings.Contains(view, "Ready to chat") && !strings.Contains(view, "Thinking")
	})
	if requests.Load() != 1 || calls.Load() != 0 || strings.Contains(u.model.View(), "Unknown command") {
		t.Fatal("credential lookup caused model or MCP work")
	}
}

func TestQuestionSelector(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	op := bridge.Operation{ID: "operation-1", Status: "waiting", UpdatedAt: time.Now(), Questions: []bridge.Question{
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
				payload = map[string]any{"events": []any{}, "next_cursor": ""}
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
				op.UpdatedAt = time.Now()
				op.Questions[1].Status = bridge.QuestionAnswered
				op.Questions[1].Answer = args.Answer
				op.Questions[0].ExpiresAt = time.Now().Add(500 * time.Millisecond)
				payload = op
			}
			encoded, _ := json.Marshal(payload)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
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
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil
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

func TestReadingReportDoesNotTriggerBackgroundReview(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var reads, requests atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "report-fixture", Version: "1"}, nil)
	for _, name := range []string{"get_report", "get_operation", "operation_events"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			payload := `{"operation_id":"finished-operation","output":{"summary":"Saved review"}}`
			if req.Params.Name != "get_report" {
				reads.Add(1)
				payload = `{"id":"finished-operation","status":"completed"}`
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: payload}}}, nil
		})
	}
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer endpoint.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			writeModelDelta(w, toolDelta("get_report", map[string]any{"operation_id": "finished-operation"}), "tool_calls")
			return
		}
		writeModelDelta(w, map[string]any{"content": "Saved review retrieved"}, "stop")
	}))
	defer provider.Close()
	u := newUI(t, ctx, endpoint.URL, provider.URL)
	defer u.model.Close()

	u.text("Get the saved report")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("report reply", func(view string) bool {
		return strings.Contains(view, "Saved review retrieved") && !strings.Contains(view, "Thinking")
	})
	// Drain asynchronous updates: a report read must not launch a watcher or another model turn.
	quiet := time.NewTimer(250 * time.Millisecond)
	defer quiet.Stop()
wait:
	for {
		select {
		case msg := <-u.messages:
			_, cmd := u.model.Update(msg)
			u.run(cmd)
		case <-quiet.C:
			break wait
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}

	if reads.Load() != 0 || requests.Load() != 2 {
		t.Fatalf("report read started background work: %d operation reads, %d model requests", reads.Load(), requests.Load())
	}
	if strings.Contains(u.model.View(), "Operator input") {
		t.Fatal("report retrieval opened an operator question")
	}
}

func TestPreflightUpdatesReachModelWithoutRepeatingUnchangedRequirements(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var snapshot atomic.Value
	var reads, requests, eventPages atomic.Int32
	const eventCursor = "opaque:event-page/token+1="
	op := bridge.Operation{ID: "operation-1", Status: "waiting", UpdatedAt: time.Now(), Preflight: &bridge.Preflight{Ready: false, Version: 1}, Events: []json.RawMessage{json.RawMessage(`{"sequence":42}`)}}
	publish := func() {
		encoded, err := json.Marshal(op)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Store(json.RawMessage(encoded))
	}
	publish()
	server := mcp.NewServer(&mcp.Implementation{Name: "preflight-fixture", Version: "1"}, nil)
	for _, name := range []string{"get_operation", "operation_events"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			payload := snapshot.Load()
			if req.Params.Name == "operation_events" {
				var args struct {
					Cursor      string `json:"cursor"`
					WaitSeconds int    `json:"wait_seconds"`
				}
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return nil, err
				}
				wantCursor, wantWait := eventCursor, 25
				if eventPages.Add(1) == 1 {
					wantCursor, wantWait = "", 0
				}
				if args.Cursor != wantCursor || args.WaitSeconds != wantWait {
					t.Errorf("event request = %+v, want cursor %q and wait_seconds %d", args, wantCursor, wantWait)
				}
				payload = map[string]any{"events": []any{}, "next_cursor": eventCursor}
			} else {
				reads.Add(1)
			}
			encoded, _ := json.Marshal(payload)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
		})
	}
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer endpoint.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
			t.Error("missing model messages")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		last := body.Messages[len(body.Messages)-1].Content
		if !strings.Contains(last, `"preflight":`) {
			t.Errorf("model did not receive preflight: %s", last)
		}
		message := "Requirements need operator attention"
		if requests.Add(1) > 1 {
			message = "Environment ready to run"
		}
		writeModelDelta(w, map[string]any{"content": message}, "stop")
	}))
	defer provider.Close()
	u := newUI(t, ctx, endpoint.URL, provider.URL)
	defer u.model.Close()

	u.text("/watch operation-1")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("blocked preflight", func(view string) bool {
		return strings.Contains(view, "requirements missing") && strings.Contains(view, "Requirements need operator attention") && !strings.Contains(view, "Thinking")
	})
	op.UpdatedAt = time.Now()
	publish()
	targetReads := reads.Load() + 3
	u.wait("unchanged preflight", func(string) bool { return reads.Load() >= targetReads })
	if requests.Load() != 1 {
		t.Fatalf("unchanged requirements triggered %d model calls", requests.Load())
	}
	op.Status, op.Preflight.Ready = "planning", true
	op.UpdatedAt = time.Now()
	op.Preflight.Version++
	publish()
	u.wait("ready preflight", func(view string) bool {
		return strings.Contains(view, "Environment ready to run") && !strings.Contains(view, "Thinking")
	})
	if requests.Load() != 2 {
		t.Fatalf("expected one review per preflight change, got %d", requests.Load())
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
	return newUIWithConfig(t, ctx, ui.Config{Provider: "other", Model: "fixture", BaseURL: provider, MCPURL: endpoint, Keys: map[string]string{"other": "test-only"}, NoColor: true})
}

func newUIWithConfig(t *testing.T, ctx context.Context, config ui.Config) *uiDriver {
	t.Helper()
	u := &uiDriver{t: t, ctx: ctx, messages: make(chan tea.Msg, 32), model: ui.New(ctx, config, io.Discard)}
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
