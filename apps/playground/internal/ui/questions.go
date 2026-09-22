package ui

import (
	"fmt"
	"strings"
	"time"

	bridge "commander/apps/playground/internal/mcp"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type questionKey struct{ operationID, questionID string }

type pendingQuestion struct {
	key      questionKey
	question bridge.Question
}

type questionPanel struct {
	current                      *pendingQuestion
	remaining                    int
	selection                    int
	focused, editing, submitting bool
	input                        textinput.Model
	error                        string
	answered                     map[questionKey]bool
}

func (m *Model) initQuestions() {
	m.questions.input = m.input
	m.questions.input.Prompt = "Answer › "
	m.questions.input.Placeholder = "Type your answer…"
	m.questions.input.Reset()
	m.questions.input.Blur()
	m.questions.answered = make(map[questionKey]bool)
}

func (m *Model) pendingQuestions(operation bridge.Operation, now time.Time) []pendingQuestion {
	if operation.Terminal() {
		return nil
	}
	questions := append([]bridge.Question(nil), operation.Questions...)
	if last := operation.LastQuestion; last != nil {
		found := false
		for i := range questions {
			if questions[i].ID == last.ID {
				questions[i] = *last
				found = true
				break
			}
		}
		if !found {
			questions = append(questions, *last)
		}
	}
	var pending []pendingQuestion
	for _, question := range questions {
		key := questionKey{operation.ID, question.ID}
		if question.Pending(now) && !m.questions.answered[key] {
			pending = append(pending, pendingQuestion{key, question})
		}
	}
	return pending
}

func (m *Model) syncQuestion(now time.Time) bool {
	var latest *pendingQuestion
	total := 0
	for _, operation := range m.operations {
		pending := m.pendingQuestions(operation, now)
		count := len(pending)
		if len(operation.Questions) == 0 && count > 0 {
			count = max(count, operation.RemainingQuestions+1)
		}
		total += count
		for _, candidate := range pending {
			if latest == nil || candidate.question.AskedAt.After(latest.question.AskedAt) ||
				(candidate.question.AskedAt.Equal(latest.question.AskedAt) &&
					(candidate.key.operationID > latest.key.operationID || candidate.key.operationID == latest.key.operationID && candidate.key.questionID > latest.key.questionID)) {
				copy := candidate
				latest = &copy
			}
		}
	}
	old := m.questions.current
	changed := (old == nil) != (latest == nil) || (old != nil && latest != nil && old.key != latest.key)
	m.questions.current = latest
	m.questions.remaining = max(0, total-1)
	if changed {
		m.questions.selection = 0
		m.questions.input.Reset()
		m.questions.error = ""
		m.questions.submitting = false
		m.questions.editing = latest != nil && len(latest.question.Options) == 0
		m.focusQuestion(latest != nil)
	}
	return changed
}

func (m *Model) focusQuestion(focused bool) {
	m.questions.focused = focused && m.questions.current != nil
	if m.questions.focused {
		m.input.Blur()
		if m.questions.editing {
			m.questions.input.Focus()
		}
	} else {
		m.questions.input.Blur()
		m.input.Focus()
	}
}

func (m *Model) questionKey(key tea.KeyMsg) bool {
	panel := &m.questions
	if m.stage != stageChat || panel.current == nil {
		return false
	}
	if key.String() == "tab" {
		m.focusQuestion(!panel.focused)
		return true
	}
	if !panel.focused || key.String() == "ctrl+c" || key.String() == "ctrl+d" || key.String() == "pgup" || key.String() == "pgdown" {
		return false
	}
	// Slash commands always address chat and leave both drafts intact.
	if key.Type == tea.KeyRunes && strings.HasPrefix(string(key.Runes), "/") && panel.input.Value() == "" {
		m.focusQuestion(false)
		return false
	}
	if panel.submitting {
		return true
	}
	options := min(4, len(panel.current.question.Options))
	switch key.String() {
	case "esc":
		panel.editing = false
		panel.input.Blur()
		return true
	case "up", "down":
		if !panel.editing {
			if key.String() == "up" {
				panel.selection = max(0, panel.selection-1)
			} else {
				panel.selection = min(options, panel.selection+1)
			}
			return true
		}
	case "enter":
		answer := panel.input.Value()
		if !panel.editing && panel.selection < options {
			answer = panel.current.question.Options[panel.selection]
		} else if !panel.editing {
			panel.editing = true
			panel.input.Focus()
			return true
		}
		if strings.TrimSpace(answer) == "" {
			panel.error = "Enter an answer."
			return true
		}
		if err := m.engine.AnswerQuestion(panel.current.key.operationID, panel.current.key.questionID, answer); err != nil {
			panel.error = err.Error()
		} else {
			panel.submitting = true
			panel.error = ""
		}
		return true
	}
	if key.Type == tea.KeyRunes && !panel.editing {
		panel.editing = true
		panel.selection = options
		panel.input.Focus()
	}
	if panel.editing {
		panel.input, _ = panel.input.Update(key)
	}
	return true
}

func (m *Model) questionView(now time.Time) string {
	panel := &m.questions
	if m.stage != stageChat || panel.current == nil || !panel.current.question.Pending(now) {
		return ""
	}
	question := panel.current.question
	operationID := panel.current.key.operationID
	if len(operationID) > 8 {
		operationID = operationID[:8]
	}
	header := "✷ Operator input · operation " + clean(operationID)
	if !question.ExpiresAt.IsZero() {
		seconds := max(0, int((question.ExpiresAt.Sub(now)+time.Second-1)/time.Second))
		header += fmt.Sprintf(" · %d:%02d left", seconds/60, seconds%60)
	}
	lines := []string{m.color(header, ReticleGreen), ansi.Wrap(clean(question.Text), max(20, m.width-4), " ")}
	if panel.remaining > 0 {
		lines = append(lines, m.color(fmt.Sprintf("%d other question(s) waiting", panel.remaining), MutedGray))
	}
	if panel.editing {
		lines = append(lines, panel.input.View())
	} else {
		choices := append([]string{}, question.Options[:min(4, len(question.Options))]...)
		choices = append(choices, "Write an answer…")
		for i, choice := range choices {
			label := "  " + ansi.Truncate(clean(choice), max(1, m.width-6), "…")
			if i == panel.selection {
				label = "› " + strings.TrimPrefix(label, "  ")
				if panel.focused {
					label = m.color(label, ReticleGreen)
				}
			}
			lines = append(lines, label)
		}
	}
	if panel.error != "" {
		lines = append(lines, m.color("Answer failed: "+clean(panel.error), AssaultRed))
	}
	hint := "↑/↓ choose · Enter answer · type your own · Tab to chat"
	if !panel.focused {
		hint = "Chat focused · Tab to answer"
	} else if panel.submitting {
		hint = stars[m.frame%len(stars)] + " Sending answer… · Tab to chat"
	} else if panel.editing {
		hint = "Enter answer · Esc choices · Tab to chat"
	}
	lines = append(lines, m.color(hint, MutedGray))
	return strings.Join(lines, "\n")
}
