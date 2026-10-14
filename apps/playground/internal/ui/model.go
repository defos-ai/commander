package ui

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/defos-ai/commander/apps/playground/internal/chat"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/host"
	"github.com/muesli/termenv"
)

type Config struct {
	Provider, Model, BaseURL, MCPURL string
	AdminURL, AdminHint              string
	Keys                             map[string]string
	NoColor                          bool
	Debug                            bool
	Notices                          []string
	LocalAPI                         *LocalAPI
	MCPCredentials                   host.BearerCredentials
}

type LocalAPI struct {
	URL, Username, Password string
}
type entry struct {
	kind, text    string
	rendered      string
	renderedWidth int
	dirty         bool
}
type tick time.Time
type eventMessage struct {
	event  chat.Event
	closed bool
}

type Model struct {
	setupState
	questions            questionPanel
	ctx                  context.Context
	config               Config
	input                textinput.Model
	viewport             viewport.Model
	width, height, frame int
	entries              []entry
	commander            *bridge.Client
	engine               *chat.Engine
	busy                 bool
	activity             string
	operations           map[string]bridge.Operation
	streamIndex          int
	forceBottom          bool
	renderer             *lipgloss.Renderer
	controls             int
	markdown             *glamour.TermRenderer
	markdownWidth        int
}

func New(ctx context.Context, config Config, output io.Writer) *Model {
	renderer := lipgloss.NewRenderer(output)
	profile := termenv.TrueColor
	if config.NoColor {
		profile = termenv.Ascii
	}
	renderer.SetColorProfile(profile)
	// Explicit colors avoid terminal probes competing with the keyboard reader.
	renderer.SetHasDarkBackground(true)
	input := textinput.New()
	input.Prompt = "› "
	input.CharLimit = 16000
	input.Width = 74
	input.Focus()
	m := &Model{ctx: ctx, config: config, input: input, viewport: viewport.New(78, 16), width: 80, height: 28, operations: map[string]bridge.Operation{}, streamIndex: -1, renderer: renderer}
	m.styleInput()
	m.initQuestions()
	m.setStage(stageProvider, "/help, /back, or /exit", "")
	return m
}
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, nextTick()}
	if m.config.Provider != "" {
		cmds = append(cmds, func() tea.Msg { return initialProvider(m.config.Provider) })
	}
	return tea.Batch(cmds...)
}

func nextTick() tea.Cmd {
	return tea.Tick(140*time.Millisecond, func(t time.Time) tea.Msg { return tick(t) })
}

func (m *Model) Close() {
	if m.setupCancel != nil {
		m.setupCancel()
	}
	if m.engine != nil {
		m.engine.Close()
	}
	if m.commander != nil {
		_ = m.commander.Close()
	}
}

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, handled := m.setupResult(message); handled {
		return m, cmd
	}
	if m.stage == stageChat && m.syncQuestion(time.Now()) {
		m.refresh()
		if key, ok := message.(tea.KeyMsg); ok && key.String() == "enter" {
			// An expired question may reveal a different one. Show that change
			// before accepting an answer intended for the previous question.
			return m, nil
		}
	}
	switch msg := message.(type) {
	case tick:
		m.frame++
		return m, nextTick()
	case tea.WindowSizeMsg:
		m.width, m.height = max(24, msg.Width), max(12, msg.Height)
		m.input.Width = max(12, m.width-8)
		m.questions.input.Width = max(12, m.width-12)
		m.refresh()
		return m, nil
	case eventMessage:
		if msg.closed {
			return m, tea.Quit
		}
		m.receive(msg.event)
		return m, waitEvent(m.engine)
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		var direction tea.KeyType
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			direction = tea.KeyUp
		case tea.MouseButtonWheelDown:
			direction = tea.KeyDown
		default:
			return m, nil
		}
		if m.selecting() {
			m.navigate(tea.KeyMsg{Type: direction})
		} else if m.stage == stageChat {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		if m.questionKey(msg) {
			m.refresh()
			return m, nil
		}
		if m.selecting() && m.navigate(msg) {
			return m, nil
		}
		switch msg.String() {
		case "ctrl+d":
			return m, tea.Quit
		case "ctrl+c":
			if m.stage != stageChat {
				return m, tea.Quit
			}
			m.input.Reset()
			if m.engine != nil {
				_ = m.engine.Send("/stop")
			}
			return m, nil
		case "enter":
			value := strings.TrimSpace(m.input.Value())
			if value == "/exit" {
				return m, tea.Quit
			}
			if m.localCommand(value) {
				return m, nil
			}
			if m.stage != stageChat {
				return m, m.setupInput(value)
			}
			m.input.Reset()
			if value != "" {
				m.add("user", value)
				if err := m.engine.Send(value); err != nil {
					m.add("error", err.Error())
				}
			}
			return m, nil
		case "pgup", "pgdown", "ctrl+u":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}
	var cmd tea.Cmd
	if m.questions.focused && m.questions.editing {
		m.questions.input, cmd = m.questions.input.Update(message)
		return m, cmd
	}
	m.input, cmd = m.input.Update(message)
	return m, cmd
}

func waitEvent(engine *chat.Engine) tea.Cmd {
	return func() tea.Msg { event, ok := <-engine.Events(); return eventMessage{event, !ok} }
}

func (m *Model) receive(event chat.Event) {
	switch event.Kind {
	case "control_start":
		m.controls++
	case "control_end":
		m.controls = max(0, m.controls-1)
	case "busy":
		m.busy = true
		m.activity = event.Text
	case "idle":
		m.busy = false
		m.activity = ""
		m.streamIndex = -1
	case "tool":
		m.activity = "Running " + event.Text
		m.add("tool", event.Text)
	case "response":
		m.streamIndex = -1
		m.activity = "Thinking"
	case "response_end":
		m.streamIndex = -1
	case "delta":
		if m.streamIndex < 0 {
			m.add("assistant", "")
			m.streamIndex = len(m.entries) - 1
		}
		m.entries[m.streamIndex].text += event.Text
		m.entries[m.streamIndex].dirty = true
		m.refresh()
	case "operation":
		if event.Operation != nil {
			previous, exists := m.operations[event.ID]
			if !exists || !event.Operation.UpdatedAt.Before(previous.UpdatedAt) {
				m.operations[event.ID] = *event.Operation
				m.syncQuestion(time.Now())
				m.refresh()
			}
		}
	case "question":
		// Pending questions are rendered from current operation snapshots.
	case "answer_start":
		if current := m.questions.current; current != nil && current.key == (questionKey{event.OperationID, event.QuestionID}) {
			m.questions.submitting = true
		}
	case "answer_end":
		key := questionKey{event.OperationID, event.QuestionID}
		if event.Text == "" {
			if event.Answer != "" && !m.questions.answered[key] {
				m.add("user", "Operator answer: "+event.Answer)
			}
			m.questions.answered[key] = true
			m.syncQuestion(time.Now())
		} else if current := m.questions.current; current != nil && current.key == key {
			m.questions.submitting = false
			m.questions.error = event.Text
		} else {
			m.add("error", "Answer failed: "+event.Text)
		}
		m.refresh()
	default:
		m.add(event.Kind, event.Text)
	}
}
func (m *Model) add(kind, text string) {
	// Setup failures belong to the current prompt, not the chat history.
	if kind == "error" && m.stage != stageChat {
		m.setupError = clean(text)
		return
	}
	m.entries = append(m.entries, entry{kind: kind, text: clean(text), dirty: true})
	if len(m.entries) > 500 {
		m.entries = m.entries[len(m.entries)-500:]
		m.streamIndex = -1
	}
	m.forceBottom = true
	m.refresh()
}
func clean(text string) string {
	text = ansi.Strip(text)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 32 && r != 127 && !(r >= 0x80 && r <= 0x9f)) {
			return r
		}
		return -1
	}, text)
}
