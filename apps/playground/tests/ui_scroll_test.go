package playground_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestChatMouseScrolling(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	server := mcp.NewServer(&mcp.Implementation{Name: "scroll-fixture", Version: "1"}, nil)
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer endpoint.Close()
	var modelRequests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelRequests.Add(1)
		http.Error(w, "scrolling must not submit model work", http.StatusBadRequest)
	}))
	defer provider.Close()
	u := newUI(t, ctx, endpoint.URL, provider.URL)
	defer u.model.Close()
	u.model.Update(tea.WindowSizeMsg{Width: 110, Height: 18})
	u.text("/help")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("help content", func(view string) bool { return strings.Contains(view, "Ctrl+D exits.") })
	const draft = "Keep this draft while I read the help"
	u.text(draft)
	bottom := u.model.View()
	for _, msg := range []tea.MouseMsg{
		{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionRelease},
		{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{Button: tea.MouseButtonWheelLeft, Action: tea.MouseActionPress},
	} {
		u.model.Update(msg)
		if u.model.View() != bottom {
			t.Fatal("a click, horizontal wheel or release scrolled the chat or changed its input")
		}
	}
	for range 30 {
		u.model.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress, X: 10, Y: 5})
	}
	top := u.model.View()
	if !strings.Contains(top, "Commander connected") || strings.Contains(top, "Ctrl+D exits.") || !strings.Contains(top, draft) {
		t.Fatal("wheel up did not reach earlier chat content while preserving the draft")
	}
	u.model.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if u.model.View() != top {
		t.Fatal("wheel up moved past the start of the chat")
	}
	for range 30 {
		u.model.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: 10, Y: 5})
	}
	if u.model.View() != bottom {
		t.Fatal("wheel down did not restore the latest content and unchanged draft")
	}
	u.key(tea.KeyMsg{Type: tea.KeyPgUp})
	if u.model.View() == bottom || !strings.Contains(u.model.View(), draft) {
		t.Fatal("PgUp stopped scrolling or changed the draft after mouse scrolling")
	}
	u.key(tea.KeyMsg{Type: tea.KeyPgDown})
	if u.model.View() != bottom || modelRequests.Load() != 0 {
		t.Fatal("PgDown did not restore the latest content or scrolling submitted model work")
	}
}

func TestProviderSelectorMouseScrolling(t *testing.T) {
	model := ui.New(t.Context(), ui.Config{NoColor: true}, io.Discard)
	defer model.Close()
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	model.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if !strings.Contains(model.View(), "› OpenAI") {
		t.Fatal("wheel up moved past the first provider")
	}
	model.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if !strings.Contains(model.View(), "› Anthropic") || !strings.Contains(model.View(), "Select your model provider:") {
		t.Fatal("wheel down did not highlight the next provider without selecting it")
	}
	selected := model.View()
	for _, msg := range []tea.MouseMsg{
		{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionRelease},
		{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{Button: tea.MouseButtonWheelRight, Action: tea.MouseActionPress},
	} {
		model.Update(msg)
		if model.View() != selected {
			t.Fatal("a click, horizontal wheel or release changed provider selection")
		}
	}
	model.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if !strings.Contains(model.View(), "› OpenAI") {
		t.Fatal("wheel up did not return to the previous provider")
	}
	for range 20 {
		model.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	}
	if !strings.Contains(model.View(), "› Other (OpenAI-compatible)") {
		t.Fatal("wheel down did not reach the last provider")
	}
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(model.View(), "API base URL") || strings.Contains(model.View(), "Select your model provider:") {
		t.Fatal("Enter did not select the provider highlighted with the mouse wheel")
	}
}
