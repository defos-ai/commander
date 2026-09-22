package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	AssaultRed    = "#D00C0C"
	ReticleGreen  = "#00AD67"
	SmokeWhite    = "#ECEBE8"
	StealthBlack  = "#282828"
	ArmorGraphite = "#333333"
	MutedGray     = "#777777"
)

var stars = []string{"✸", "✷", "✯", "✷"}

func (m *Model) color(text, foreground string) string {
	if m.config.NoColor {
		return text
	}
	var color lipgloss.TerminalColor = lipgloss.Color(foreground)
	if foreground == SmokeWhite {
		color = lipgloss.AdaptiveColor{Light: ArmorGraphite, Dark: SmokeWhite}
	}
	return m.renderer.NewStyle().Foreground(color).Render(text)
}
func (m *Model) styleInput() {
	m.input.PromptStyle = m.renderer.NewStyle()
	m.input.TextStyle = m.renderer.NewStyle()
	m.input.PlaceholderStyle = m.renderer.NewStyle()
	m.input.CompletionStyle = m.renderer.NewStyle()
	m.input.Cursor.Style = m.renderer.NewStyle()
	m.input.Cursor.TextStyle = m.renderer.NewStyle()
	if m.config.NoColor {
		return
	}
	base := m.renderer.NewStyle().Background(lipgloss.Color(StealthBlack)).Foreground(lipgloss.Color(SmokeWhite))
	m.input.TextStyle = base
	m.input.PromptStyle = base.Foreground(lipgloss.Color(ReticleGreen))
	m.input.PlaceholderStyle = base.Foreground(lipgloss.Color(MutedGray))
	m.input.CompletionStyle = m.input.PlaceholderStyle
	m.input.Cursor.TextStyle = base
	m.input.Cursor.Style = base.Foreground(lipgloss.Color(ReticleGreen))
}
func (m *Model) refresh() {
	bottom := m.forceBottom || m.viewport.AtBottom()
	m.forceBottom = false
	m.viewport.Width = max(20, m.width-4)
	activityLines := min(len(m.operations), 4)
	m.viewport.Height = max(3, m.height-10-activityLines)
	var lines []string
	for _, item := range m.entries {
		label, color := "", SmokeWhite
		switch item.kind {
		case "user":
			label, color = "You", ReticleGreen
		case "assistant":
			label = "Commander"
		case "error":
			label, color = "✸", AssaultRed
		case "success":
			label, color = "✓", ReticleGreen
		case "question":
			label, color = "✷ Operator input", ReticleGreen
		case "tool":
			label = "↳"
		case "notice":
			label = "·"
		}
		text := m.color(label, color) + "  " + item.text
		if item.kind == "user" || item.kind == "assistant" || item.kind == "question" {
			text = m.color(label, color) + "\n" + item.text + "\n"
		}
		lines = append(lines, m.renderer.NewStyle().Width(m.viewport.Width).Render(text))
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
	if bottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) View() string {
	header := "✸ DEFOS | Commander Playground"
	if m.stage == "chat" {
		header += "  ·  " + clean(m.settings.Model)
	}
	if !m.config.NoColor {
		header = ansi.Truncate(header, max(1, m.width-2), "…")
		header = m.renderer.NewStyle().Background(lipgloss.Color(ArmorGraphite)).Foreground(lipgloss.Color(ReticleGreen)).Padding(0, 1).Width(m.width).Render(header)
	} else {
		header = ansi.Truncate(header, m.width, "…")
	}
	var content string
	if m.stage == "chat" {
		content = m.viewport.View()
	} else {
		var lines []string
		switch m.stage {
		case "provider", "model":
			lines = m.selectionView()
		case "url":
			lines = append(lines, "API base URL")
		case "key":
			lines = append(lines, m.provider.Name+" API key", "Kept in memory for this session.")
		case "manual_model":
			lines = append(lines, "Model name", "Choose a chat model that supports tool calling.")
		case "mcp":
			lines = append(lines, "Commander MCP URL")
		}
		start := max(0, len(m.entries)-3)
		for _, item := range m.entries[start:] {
			color := SmokeWhite
			if item.kind == "error" {
				color = AssaultRed
			}
			lines = append(lines, "", m.color(item.text, color))
		}
		content = strings.Join(lines, "\n")
	}
	var activity []string
	if m.busy {
		activity = append(activity, m.color(stars[m.frame%len(stars)], ReticleGreen)+" "+m.activity)
	} else if m.controls > 0 {
		activity = append(activity, m.color(stars[m.frame%len(stars)], ReticleGreen)+" Loading Commander data")
	}
	ids := make([]string, 0, len(m.operations))
	for id := range m.operations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, id := range ids {
		if i == 4 {
			activity = append(activity, fmt.Sprintf("  +%d more · /operations", len(ids)-4))
			break
		}
		op := m.operations[id]
		done := 0
		pending := 0
		for _, task := range op.Tasks {
			if task.Status != "running" && task.Status != "queued" {
				done++
			}
		}
		for _, question := range op.Questions {
			if question.AnsweredAt == nil {
				pending++
			}
		}
		marker := stars[m.frame%len(stars)]
		if op.Settled() {
			marker = "✓"
		}
		color := ReticleGreen
		if op.Status == "failed" {
			color = AssaultRed
		}
		label := id
		if len(label) > 8 {
			label = label[:8]
		}
		line := fmt.Sprintf("%s %s · %s · %d/%d tasks finished", m.color(marker, color), label, op.Status, done, len(op.Tasks))
		if pending > 0 {
			line += fmt.Sprintf(" · %d question(s)", pending)
		}
		activity = append(activity, line)
	}
	input := m.input.View()
	if !m.config.NoColor {
		input = m.renderer.NewStyle().Background(lipgloss.Color(StealthBlack)).Foreground(lipgloss.Color(SmokeWhite)).Padding(0, 1).Render(input)
	}
	if m.selecting() && m.input.Value() == "" {
		input = m.color("↑/↓ choose · Enter select", MutedGray)
	}
	foot := "/help · /exit"
	if m.stage == "chat" {
		foot += " · PgUp/PgDown scroll · Ctrl+C stop reply"
	} else {
		foot += " · /back"
	}
	return header + "\n\n" + content + "\n\n" + strings.Join(activity, "\n") + "\n" + input + "\n" + m.color(foot, MutedGray) + "\n"
}
