package ui_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"commander/apps/playground/internal/ui"
	"commander/apps/playground/tests/fixture"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func key(model *ui.Model, kind tea.KeyType) tea.Cmd {
	_, cmd := model.Update(tea.KeyMsg{Type: kind})
	return cmd
}

func typeText(model *ui.Model, value string) tea.Cmd {
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)})
	return key(model, tea.KeyEnter)
}

func awaitMessage(t *testing.T, messages <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("UI command did not finish")
		return nil
	}
}

func TestSetupBackCancelsDiscoveryAndIgnoresItsLateResult(t *testing.T) {
	// Arrange: the first discovery request stays open until /back cancels it.
	started := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"new-model"}]}`)
	}))
	defer server.Close()
	model := ui.New(t.Context(), ui.Config{BaseURL: server.URL, Keys: map[string]string{"other": "secret"}, NoColor: true}, io.Discard)
	defer model.Close()
	key(model, tea.KeyEnd)
	discover := key(model, tea.KeyEnter)
	oldResult := make(chan tea.Msg, 1)
	go func() { oldResult <- discover() }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not start")
	}

	// Act: restart setup, accept a fresh result, then deliver the cancelled result.
	typeText(model, "/back")
	late := awaitMessage(t, oldResult)
	key(model, tea.KeyEnd)
	fresh := key(model, tea.KeyEnter)
	model.Update(fresh())
	model.Update(late)

	// Assert: stale discovery cannot replace the active provider/model selection.
	view := model.View()
	if !strings.Contains(view, "Select a model:") || !strings.Contains(view, "new-model") || strings.Contains(view, "discovery failed") {
		t.Fatalf("stale discovery changed the setup: %s", view)
	}
}

func TestSetupMasksTypedAPIKey(t *testing.T) {
	// Arrange: select OpenAI without a saved key.
	model := ui.New(t.Context(), ui.Config{NoColor: true}, io.Discard)
	defer model.Close()
	key(model, tea.KeyEnter)

	// Act: type a credential without submitting it.
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("never-display-this-key")})

	// Assert: the setup remains usable without echoing the credential.
	view := model.View()
	if !strings.Contains(view, "OpenAI API key") || strings.Contains(view, "never-display-this-key") {
		t.Fatalf("credential input was not masked: %s", view)
	}
}

func TestAssistantMarkdownStreamsAndReflowsWithoutTerminalControls(t *testing.T) {
	for _, noColor := range []bool{true, false} {
		t.Run(fmt.Sprintf("no_color=%t", noColor), func(t *testing.T) {
			testAssistantMarkdown(t, noColor)
		})
	}
}

func testAssistantMarkdown(t *testing.T, noColor bool) {
	// Arrange: real chat/model/MCP clients, with the model paused between chunks.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resume := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.SSE(w, map[string]any{"content": "# Review\n\n- **Accessible**\n- Read [guide](https://example.test/guide)\n\n```go\nfmt.Println(\"ok\")\n```\n\n| Check | Status |\n| --- | --- |\n| SEO | Ready |\n\n"}, "")
		select {
		case <-resume:
		case <-r.Context().Done():
			return
		}
		fixture.SSE(w, map[string]any{"content": "\x1b]52;c;ZXZpbA==\a\x1b[2J&#27;]52;c;encoded-secret&#7;Completed."}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	model := ui.New(ctx, ui.Config{BaseURL: server.URL, Model: "fixture-chat", MCPURL: fixture.MCP(t, nil), Keys: map[string]string{"other": "secret"}, NoColor: noColor, Notices: []string{"Sample website: http://localhost:8080"}}, io.Discard)
	defer model.Close()
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	key(model, tea.KeyEnd)
	connect := key(model, tea.KeyEnter)
	_, next := model.Update(connect())
	if !strings.Contains(model.View(), "Sample website: http://localhost:8080") {
		t.Fatal("startup notice was not displayed")
	}
	typeText(model, "Review the website")
	advance := func() {
		t.Helper()
		messages := make(chan tea.Msg, 1)
		go func(cmd tea.Cmd) { messages <- cmd() }(next)
		_, next = model.Update(awaitMessage(t, messages))
	}

	// Act: observe Markdown while the reply is still streaming, then resize it.
	for !strings.Contains(model.View(), "SEO") {
		advance()
	}
	view := model.View()
	for _, want := range []string{"Review", "Accessible", "https://example.test/guide", `fmt.Println("ok")`, "Ready"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing rendered Markdown %q: %s", want, view)
		}
	}
	for _, raw := range []string{"# Review", "**Accessible**", "[guide](", "```", "| --- |"} {
		if strings.Contains(view, raw) {
			t.Errorf("Markdown syntax was not rendered: %q", raw)
		}
	}
	model.Update(tea.WindowSizeMsg{Width: 50, Height: 60})
	close(resume)
	for !strings.Contains(model.View(), "Completed.") {
		advance()
	}

	// Assert: content survives reflow; only application-generated styling is allowed.
	view = model.View()
	plain := ansi.Strip(view)
	if (noColor && strings.Contains(view, "\x1b")) || strings.Contains(view, "\x1b]") || strings.Contains(view, "ZXZpbA") || strings.Contains(view, "[2J") || !strings.Contains(plain, "Accessible") || !strings.Contains(plain, `fmt.Println("ok")`) {
		t.Fatalf("unsafe or missing rendered content: %q", view)
	}
}
