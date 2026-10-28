package playground_test

import (
	"fmt"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/chat"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
)

func TestSetupCompactHelpRemainsLocal(t *testing.T) {
	for _, allInOne := range []bool{false, true} {
		t.Run(fmt.Sprintf("all-in-one=%t", allInOne), func(t *testing.T) {
			model := ui.New(t.Context(), ui.Config{NoColor: true, AllInOne: allInOne}, io.Discard)
			defer model.Close()
			model.Update(tea.WindowSizeMsg{Width: 180, Height: 60})

			model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/help  ")})
			_, action := model.Update(tea.KeyMsg{Type: tea.KeyEnter})

			view := model.View()
			if action != nil || !strings.Contains(view, "Select your model provider:") ||
				!strings.Contains(view, "/admin [command] — Admin UI and commands") || !strings.Contains(view, chat.HelpDocsURL) {
				t.Fatalf("setup help left setup or omitted local commands: %s", view)
			}
			if strings.Contains(view, "/credentials") != allInOne {
				t.Fatalf("setup help advertised credentials for the wrong mode: %s", view)
			}
			for _, advanced := range []string{"/help all", "Enter model name…", "PgUp/PgDown", "Ctrl+Shift+C", "Cmd+C"} {
				if strings.Contains(view, advanced) {
					t.Fatalf("compact setup help retained %s guidance: %s", advanced, view)
				}
			}
		})
	}
}

func TestSetupInvalidHelpArgumentsShowUsage(t *testing.T) {
	for _, command := range []string{"/help all", "/help other", "/help all extra"} {
		t.Run(command, func(t *testing.T) {
			model := ui.New(t.Context(), ui.Config{NoColor: true}, io.Discard)
			defer model.Close()
			model.Update(tea.WindowSizeMsg{Width: 140, Height: 40})

			model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(command)})
			_, action := model.Update(tea.KeyMsg{Type: tea.KeyEnter})

			view := model.View()
			if action != nil || !strings.Contains(view, "Usage: /help") || !strings.Contains(view, "Select your model provider:") {
				t.Fatalf("invalid help did not stay local to setup: %s", view)
			}
		})
	}
}
