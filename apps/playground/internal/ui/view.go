package ui

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/defos-ai/commander/apps/playground/internal/chat"
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
	// Reserve the header and spacer; the footer's trailing newline leaves one empty row.
	m.viewport.Height = max(1, m.height-2-strings.Count(m.bottomView(m.operationProgress()), "\n"))
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
		case "notice", "admin":
			label = "·"
		}
		body := clean(item.text)
		if item.kind == "assistant" {
			body = m.renderMarkdown(body)
		} else if item.kind == "admin" {
			body = m.adminLink(body)
		} else if item.kind == "notice" {
			body = m.helpDocsLink(body)
		}
		text := m.color(label, color) + "  " + body
		if item.kind == "notice" && strings.Contains(body, "\n") {
			text = body
		}
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
	if m.secrets.open {
		return header + "\n\n" + m.secretView() + "\n"
	}
	var content string
	if m.stage == stageChat {
		content = m.renderer.NewStyle().PaddingLeft(2).Render(m.viewport.View())
	} else {
		var lines []string
		switch m.stage {
		case stageProvider, stageModel:
			lines = m.selectionView()
		case stageURL:
			lines = append(lines, "API base URL")
		case stageKey:
			usage := "Kept in memory for this session."
			if m.config.SaveProviderKey != nil {
				usage = "Remembered encrypted on this machine."
			}
			lines = append(lines, m.provider.Name+" API key · "+usage)
			if name := m.providerSecretName(); name != "" {
				lines = append(lines, "You can also set "+name+" before launch.")
			}
			if m.provider.KeyURL != "" {
				lines = append(lines, "Get an API key: "+m.color(m.provider.KeyURL, ReticleGreen))
			} else {
				lines = append(lines, "Get a key from your provider's dashboard. Leave empty if your server does not require one.")
			}
		case stageManualModel:
			lines = append(lines, "Model name", "Choose a chat model that supports tool calling.")
		case stageMCP:
			lines = append(lines, "Commander MCP URL")
		case stageMCPToken:
			lines = append(lines, "Commander MCP token", "Paste your token, then Enter.")
			if m.mcpCommand != "" {
				hint := "Need a token? Run in a terminal:"
				if strings.HasPrefix(m.mcpCommand, "/") {
					hint = "Need a token? Run in Playground:"
				}
				lines = append(lines, "", hint, m.color(m.mcpCommand, ReticleGreen))
				if command := tokenEnvironmentCommand(m.mcpCommand); command != "" {
					lines = append(lines, "", "For credential persistence in this shell, run:", m.color(command, ReticleGreen))
				}
			}
		}
		start := max(0, len(m.entries)-3)
		for _, item := range m.entries[start:] {
			color := SmokeWhite
			if item.kind == "error" {
				color = AssaultRed
			}
			text := item.text
			if item.kind == "admin" {
				text = m.adminLink(text)
			} else if item.kind == "notice" {
				text = m.helpDocsLink(text)
			}
			lines = append(lines, "", m.color(text, color))
		}
		if m.setupError != "" {
			lines = append(lines, "", m.color(m.setupError, AssaultRed))
		}
		content = strings.Join(lines, "\n")
	}
	return header + "\n\n" + content + m.bottomView(m.operationProgress())
}

func (m *Model) bottomView(progress []string) string {
	var activity []string
	if m.admin.running {
		activity = append(activity, m.color(stars[m.frame%len(stars)], ReticleGreen)+" Running Admin command")
	}
	if m.busy {
		activity = append(activity, m.color(stars[m.frame%len(stars)], ReticleGreen)+" "+m.activity)
	} else if m.controls > 0 {
		activity = append(activity, m.color(stars[m.frame%len(stars)], ReticleGreen)+" Loading Commander data")
	}
	activity = append(activity, progress...)
	input := m.input.View()
	if !m.config.NoColor {
		input = m.renderer.NewStyle().Background(lipgloss.Color(StealthBlack)).Foreground(lipgloss.Color(SmokeWhite)).Padding(0, 1).Render(input)
	}
	if m.selecting() && m.input.Value() == "" {
		input = m.color("↑/↓ or wheel · Enter select", MutedGray)
	}
	question := ""
	if panel := m.questionView(time.Now()); panel != "" {
		question = panel + "\n\n"
	}
	bottom := "\n\n"
	if len(activity) > 0 {
		bottom += ansi.Wrap(strings.Join(activity, "\n"), max(20, m.width-4), "") + "\n"
	}
	return bottom + question + input + "\n" + m.color(m.footerView(), MutedGray) + "\n"
}

func (m *Model) footerView() string {
	foot := "/help · /exit"
	if m.admin.running {
		foot += " · Ctrl+C cancel Admin"
	} else if m.stage != stageChat {
		foot += " · /back"
	} else if m.busy {
		foot += " · Ctrl+C stop reply"
	}
	return ansi.Wrap(foot, max(20, m.width), "")
}

func (m *Model) operationProgress() []string {
	var lines []string
	ids := make([]string, 0, len(m.operations))
	for id := range m.operations {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.operations[ids[i]], m.operations[ids[j]]
		if a.UpdatedAt.Equal(b.UpdatedAt) {
			return ids[i] < ids[j]
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
	// Reserve the header, three chat rows and the actual controls before adding whole rows.
	available := max(0, m.height-5-strings.Count(m.bottomView(nil), "\n"))
	more := func(count int) string {
		return ansi.Wrap(fmt.Sprintf("  +%d more · /operations", count), max(20, m.width-4), "")
	}
	for i, id := range ids[:min(3, len(ids))] {
		op := m.operations[id]
		marker := stars[m.frame%len(stars)]
		if op.Terminal() {
			marker = "○"
		}
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
		line := fmt.Sprintf("%s %s · %s · %d/%d tasks finished", m.color(marker, color), label, op.Status, op.Progress.FinishedTasks, op.Progress.TotalTasks)
		if unknown := op.Progress.TaskCounts["interrupted"]; unknown > 0 {
			label := "task outcome"
			if unknown > 1 {
				label = "task outcomes"
			}
			line += fmt.Sprintf(" · %d %s unknown", unknown, label)
		}
		needsSecrets := op.NeedsSecrets()
		if needsSecrets {
			line += " · secrets needed · /secrets"
		} else if !op.Terminal() && op.Preflight != nil && !op.Preflight.Ready {
			line += " · requirements missing"
		}
		if op.Progress.PendingQuestions > 0 {
			line += fmt.Sprintf(" · %d question(s)", op.Progress.PendingQuestions)
		}
		if (needsSecrets || op.Status == "completed" && op.Settled()) && m.config.AdminURL != "" {
			line += " · " + m.operationAdminLink(id)
		} else if needsSecrets {
			line += " · /admin for setup"
		}
		line = ansi.Wrap(line, max(20, m.width-4), "")
		required := strings.Count(line, "\n") + 1
		if remaining := len(ids) - i - 1; remaining > 0 {
			required += strings.Count(more(remaining), "\n") + 1
		}
		if required > available {
			break
		}
		lines = append(lines, line)
		available -= strings.Count(line, "\n") + 1
	}
	if remaining := len(ids) - len(lines); remaining > 0 {
		line := more(remaining)
		if strings.Count(line, "\n")+1 <= available {
			lines = append(lines, line)
		}
	}
	return lines
}

func (m *Model) operationAdminLink(id string) string {
	base, fragment, _ := strings.Cut(m.config.AdminURL, "#")
	target := strings.TrimRight(base, "/") + "/#/operations/" + url.PathEscape(id)
	if query, ok := strings.CutPrefix(fragment, "/connect?"); ok {
		// Parse the raw fragment so encoded characters in usernames and URLs
		// survive adding the destination operation.
		params, err := url.ParseQuery(query)
		if err == nil {
			params.Set("operation", id)
			target = base + "#/connect?" + params.Encode()
		}
	}
	label := "Review on Admin UI (" + base + ")"
	if m.config.NoColor {
		return "Review on Admin UI (" + target + ")"
	}
	return ansi.SetHyperlink(target) + m.color(label, ReticleGreen) + ansi.ResetHyperlink()
}

func (m *Model) adminLink(label string) string {
	target := clean(m.config.AdminURL)
	if m.config.NoColor {
		return label + " " + target
	}
	// Keep the prefill fragment in the hyperlink target so wrapping cannot
	// insert whitespace into the address a terminal opens.
	base, _, _ := strings.Cut(target, "#")
	return label + " " + ansi.SetHyperlink(target) + m.color(base, ReticleGreen) + ansi.ResetHyperlink()
}

func (m *Model) helpDocsLink(text string) string {
	if m.config.NoColor {
		return text
	}
	link := ansi.SetHyperlink(chat.HelpDocsURL) + m.color("Playground docs", ReticleGreen) + ansi.ResetHyperlink()
	return strings.ReplaceAll(text, chat.HelpDocsURL, link)
}
