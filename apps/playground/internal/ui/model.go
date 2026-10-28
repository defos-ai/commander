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
	Provider, Model, BaseURL string
	AdminURL, AdminHint      string
	Keys                     map[string]string
	NoColor                  bool
	Debug                    bool
	Notices                  []string
	API                      Connection[host.BasicCredentials]
	MCP                      Connection[host.BearerCredentials]
	AllInOne                 bool
	LocalAgentCount          int
	ServeAdmin               func(context.Context, []string) (string, error)
}

type Connection[C host.Credentials] struct {
	URL         string
	Credentials C
}
type entry struct {
	kind, text    string
	callID        string
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
	secrets              secretPanel
	admin                adminCommandState
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
	providerSecretCancel context.CancelFunc
	providerSecretDone   <-chan struct{}
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
	if m.questions.cancel != nil {
		m.questions.cancel()
		<-m.questions.done
	}
	if m.providerSecretCancel != nil {
		m.providerSecretCancel()
		<-m.providerSecretDone
	}
	if m.admin.cancel != nil {
		m.admin.cancel()
		<-m.admin.done
	}
	m.clearSecretInputs()
	if m.secrets.cancel != nil {
		m.secrets.cancel()
	}
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
	if result, ok := message.(operatorAnswerResult); ok {
		return m, m.operatorAnswerResult(result)
	}
	if cmd, handled := m.adminResult(message); handled {
		return m, cmd
	}
	if cmd, handled := m.secretResult(message); handled {
		return m, cmd
	}
	if cmd, handled := m.setupResult(message); handled {
		return m, cmd
	}
	if m.stage == stageChat && m.syncQuestion(time.Now()) {
		m.refresh()
		if key, ok := message.(tea.KeyMsg); ok && key.String() == "enter" && !m.secrets.open {
			// An expired question may reveal a different one. Show that change
			// before accepting an answer intended for the previous question.
			return m, nil
		}
	}
	switch msg := message.(type) {
	case tick:
		m.frame++
		return m, tea.Batch(nextTick(), m.syncSecrets())
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
		if msg.event.Kind == "answer_request" {
			return m, tea.Batch(waitEvent(m.engine), m.submitOperatorAnswer(msg.event))
		}
		m.receive(msg.event)
		return m, tea.Batch(waitEvent(m.engine), m.syncSecrets())
	case tea.MouseMsg:
		if m.secrets.open {
			return m, nil
		}
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
		if m.secrets.open {
			return m, m.secretKey(msg)
		}
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
			if m.admin.running {
				m.admin.cancel()
				return m, nil
			}
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
			if fields := strings.Fields(value); len(fields) > 0 && fields[0] == "/secrets" {
				m.input.Reset()
				if len(fields) != 1 || m.stage != stageChat {
					m.add("notice", "Use /secrets after connecting to Commander.")
					return m, nil
				}
				return m, m.openSecrets(true)
			}
			if value == "/exit" {
				return m, tea.Quit
			}
			if cmd, handled := m.adminCommand(value); handled {
				return m, cmd
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
		case "up", "down", "pgup", "pgdown", "ctrl+u":
			// With native terminal selection, wheel/trackpad scrolling arrives
			// as Up/Down too. Selectors and focused forms handle these first.
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}
	var cmd tea.Cmd
	if m.secrets.open {
		// Secret-owned asynchronous input is routed by secretResult. A pending
		// chat clipboard read must not populate a newly opened secret field.
		return m, nil
	}
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
		m.refresh()
	case "control_end":
		m.controls = max(0, m.controls-1)
		m.refresh()
	case "busy":
		m.busy = true
		m.activity = event.Text
		m.refresh()
	case "idle":
		m.busy = false
		m.activity = ""
		m.streamIndex = -1
		m.refresh()
	case "tool":
		m.activity = "Running " + event.Text
		m.addEntry(event.Kind, event.Text, event.CallID)
	case "success", "error":
		if !m.finishTool(event) {
			m.addEntry(event.Kind, event.Text, event.CallID)
		}
	case "response":
		m.streamIndex = -1
		m.activity = "Thinking"
		m.refresh()
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

func (m *Model) finishTool(event chat.Event) bool {
	if event.CallID == "" {
		return false
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		item := &m.entries[i]
		if item.callID == event.CallID && item.kind == "tool" {
			item.kind, item.text, item.dirty = event.Kind, clean(event.Text), true
			m.refresh()
			return true
		}
	}
	return false
}

func (m *Model) add(kind, text string) {
	m.addEntry(kind, text, "")
}

func (m *Model) addEntry(kind, text, callID string) {
	if kind == "error" && m.stage != stageChat {
		m.setupError = clean(text)
		return
	}
	m.entries = append(m.entries, entry{kind: kind, text: clean(text), callID: callID, dirty: true})
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
