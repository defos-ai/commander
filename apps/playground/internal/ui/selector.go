package ui

import (
	"fmt"
	"strings"

	"commander/apps/playground/internal/llm"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func (m *Model) selecting() bool {
	return m.stage == stageProvider || m.stage == stageModel
}

func (m *Model) choices() []string {
	if m.stage == stageProvider {
		var choices []string
		for _, provider := range llm.Presets() {
			label := provider.Name
			if m.config.Keys[provider.ID] != "" {
				label += "  ✓ key detected"
			}
			choices = append(choices, label)
		}
		return choices
	}
	choices := make([]string, 0, len(m.models)+1)
	for _, name := range m.models {
		choices = append(choices, clean(name))
	}
	return append(choices, "Enter model name…")
}

func (m *Model) visibleChoices() int {
	return min(len(m.choices()), max(1, m.height-14))
}

func (m *Model) navigate(key tea.KeyMsg) bool {
	if key.String() == "esc" {
		m.input.Reset()
		return true
	}
	if m.input.Value() != "" {
		return false
	}
	last := len(m.choices()) - 1
	switch key.String() {
	case "up":
		m.selection = max(0, m.selection-1)
	case "down":
		m.selection = min(last, m.selection+1)
	case "pgup":
		m.selection = max(0, m.selection-m.visibleChoices())
	case "pgdown":
		m.selection = min(last, m.selection+m.visibleChoices())
	case "home":
		m.selection = 0
	case "end":
		m.selection = last
	case "enter", "ctrl+c", "ctrl+d":
		return false
	default:
		return key.Type != tea.KeyRunes || !strings.HasPrefix(string(key.Runes), "/")
	}
	return true
}

func (m *Model) selectChoice() tea.Cmd {
	if m.stage == stageProvider {
		return m.selectProvider(llm.Presets()[m.selection].ID)
	}
	if m.selection == len(m.models) {
		m.setStage(stageManualModel, "Exact model name", "")
		return nil
	}
	m.settings.Model = m.models[m.selection]
	return m.connect()
}

func (m *Model) selectionView() []string {
	title := "Select your model provider:"
	if m.stage == stageModel {
		title = "Select a model:"
	}
	lines := []string{title, ""}
	choices := m.choices()
	visible := m.visibleChoices()
	start := max(0, m.selection-visible+1)
	for i := start; i < min(start+visible, len(choices)); i++ {
		label := ansi.Truncate(choices[i], max(1, m.width-4), "…")
		if i == m.selection {
			lines = append(lines, m.color("› "+label, ReticleGreen))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	if len(choices) > visible {
		lines = append(lines, m.color(fmt.Sprintf("  %d / %d · PgUp/PgDown", m.selection+1, len(choices)), MutedGray))
	}
	return lines
}
