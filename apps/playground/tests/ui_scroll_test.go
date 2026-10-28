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
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestChatScrolling(t *testing.T) {
	inputs := append(scrollInputs(), struct {
		name     string
		up, down tea.Msg
	}{
		name: "page keys",
		up:   tea.KeyMsg{Type: tea.KeyPgUp},
		down: tea.KeyMsg{Type: tea.KeyPgDown},
	})
	for _, input := range inputs {
		t.Run(input.name, func(t *testing.T) {
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
			u := fixtures.NewUI(t, ctx, endpoint.URL, provider.URL)
			defer u.Close()
			u.Model.Update(tea.WindowSizeMsg{Width: 110, Height: 18})
			u.Text("/help")
			u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			u.Wait("help content", func(view string) bool { return strings.Contains(view, "Ctrl+D exits.") })
			const draft = "Keep this draft while I read the help"
			u.Text(draft)
			bottom := u.Model.View()
			for _, msg := range []tea.Msg{
				tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionRelease},
				tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
				tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion, X: 15, Y: 3},
				tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 15, Y: 3},
				tea.MouseMsg{Button: tea.MouseButtonWheelLeft, Action: tea.MouseActionPress},
			} {
				u.Model.Update(msg)
				if u.Model.View() != bottom {
					t.Fatal("a click, drag, horizontal wheel or release scrolled the chat or changed its input")
				}
			}
			for range 30 {
				u.Model.Update(input.up)
			}
			top := u.Model.View()
			if !strings.Contains(top, "Commander connected") || strings.Contains(top, "Ctrl+D exits.") || !strings.Contains(top, draft) {
				t.Fatal("scrolling up did not reach earlier chat content while preserving the draft")
			}
			before, after := strings.Split(bottom, "\n"), strings.Split(top, "\n")
			if len(before) != len(after) || before[0] != after[0] ||
				strings.Join(before[len(before)-3:], "\n") != strings.Join(after[len(after)-3:], "\n") {
				t.Fatal("scrolling moved the header, input or footer")
			}
			u.Model.Update(input.up)
			if u.Model.View() != top {
				t.Fatal("scrolling up moved past the start of the chat")
			}
			for range 30 {
				u.Model.Update(input.down)
			}
			if u.Model.View() != bottom {
				t.Fatal("scrolling down did not restore the latest content and unchanged draft")
			}
			if modelRequests.Load() != 0 {
				t.Fatal("scrolling submitted model work")
			}
		})
	}
}

func TestProviderSelectorScrolling(t *testing.T) {
	for _, input := range scrollInputs() {
		t.Run(input.name, func(t *testing.T) {
			model := ui.New(t.Context(), ui.Config{NoColor: true}, io.Discard)
			defer model.Close()
			model.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
			model.Update(input.up)
			if !strings.Contains(model.View(), "› OpenAI") {
				t.Fatal("scrolling up moved past the first provider")
			}
			model.Update(input.down)
			if !strings.Contains(model.View(), "› Anthropic") || !strings.Contains(model.View(), "Select your model provider:") {
				t.Fatal("scrolling down did not highlight the next provider without selecting it")
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
			model.Update(input.up)
			if !strings.Contains(model.View(), "› OpenAI") {
				t.Fatal("scrolling up did not return to the previous provider")
			}
			for range 20 {
				model.Update(input.down)
			}
			if !strings.Contains(model.View(), "› Other (OpenAI-compatible)") {
				t.Fatal("scrolling down did not reach the last provider")
			}
			model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if !strings.Contains(model.View(), "API base URL") || strings.Contains(model.View(), "Select your model provider:") {
				t.Fatal("Enter did not select the highlighted provider")
			}
		})
	}
}

func scrollInputs() []struct {
	name     string
	up, down tea.Msg
} {
	return []struct {
		name     string
		up, down tea.Msg
	}{
		{
			name: "mouse events",
			up:   tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress, X: 10, Y: 5},
			down: tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: 10, Y: 5},
		},
		{
			// With mouse reporting disabled, terminal alternate scrolling
			// sends the same arrow keys as keyboard navigation.
			name: "alternate-scroll arrows",
			up:   tea.KeyMsg{Type: tea.KeyUp},
			down: tea.KeyMsg{Type: tea.KeyDown},
		},
	}
}
