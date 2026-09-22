package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

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
	// Leave ordinary text in the terminal's foreground color so both light and
	// dark terminals work without probing their background.
	if m.config.NoColor || foreground == SmokeWhite {
		return text
	}
	return m.renderer.NewStyle().Foreground(lipgloss.Color(foreground)).Render(text)
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
	questionLines := 0
	if question := m.questionView(time.Now()); question != "" {
		questionLines = strings.Count(question, "\n") + 2
	}
	m.viewport.Height = max(3, m.height-10-activityLines-questionLines)
	var lines []string
	for i := range m.entries {
		item := &m.entries[i]
		if !item.dirty && item.renderedWidth == m.viewport.Width {
			lines = append(lines, item.rendered)
			continue
		}
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
		body := clean(item.text)
		if item.kind == "assistant" {
			body = m.renderMarkdown(body)
		}
		text := m.color(label, color) + "  " + body
		if item.kind == "user" || item.kind == "assistant" || item.kind == "question" {
			text = m.color(label, color) + "\n" + body + "\n"
		}
		item.rendered = m.renderer.NewStyle().Width(m.viewport.Width).Render(text)
		item.renderedWidth, item.dirty = m.viewport.Width, false
		lines = append(lines, item.rendered)
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
	if bottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) View() string {
	header := "✸ DEFOS | Commander Playground"
	if m.stage == stageChat {
		header += "  ·  " + clean(m.settings.Model)
	}
	if !m.config.NoColor {
		header = ansi.Truncate(header, max(1, m.width-2), "…")
		header = m.renderer.NewStyle().Background(lipgloss.Color(ArmorGraphite)).Foreground(lipgloss.Color(ReticleGreen)).Padding(0, 1).Width(m.width).Render(header)
	} else {
		header = ansi.Truncate(header, m.width, "…")
	}
	var content string
	if m.stage == stageChat {
		content = m.viewport.View()
	} else {
		var lines []string
		switch m.stage {
		case stageProvider, stageModel:
			lines = m.selectionView()
		case stageURL:
			lines = append(lines, "API base URL")
		case stageKey:
			lines = append(lines, m.provider.Name+" API key", "Kept in memory for this session.")
		case stageManualModel:
			lines = append(lines, "Model name", "Choose a chat model that supports tool calling.")
		case stageMCP:
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
		pending = len(m.pendingQuestions(op, time.Now()))
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
	if m.stage == stageChat {
		foot += " · PgUp/PgDown scroll · Ctrl+C stop reply"
	} else {
		foot += " · /back"
	}
	question := ""
	if panel := m.questionView(time.Now()); panel != "" {
		question = panel + "\n\n"
	}
	return header + "\n\n" + content + "\n\n" + strings.Join(activity, "\n") + "\n" + question + input + "\n" + m.color(foot, MutedGray) + "\n"
}
