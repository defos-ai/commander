// Package ui presents Commander's setup, chat and background activity.
package ui

import (
	"context"
	"io"
	"strings"
	"time"

	"commander/apps/playground/internal/chat"
	"commander/apps/playground/internal/llm"
	bridge "commander/apps/playground/internal/mcp"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

type Config struct {
	Provider, Model, BaseURL, MCPURL string
	Keys                             map[string]string
	NoColor                          bool
	Debug                            bool
}
type entry struct{ kind, text string }
type tick time.Time
type modelsResult struct {
	generation int
	names      []string
	err        error
}
type connected struct {
	generation int
	client     *bridge.Client
	err        error
}
type eventMessage struct {
	event  chat.Event
	closed bool
}

type Model struct {
	ctx                  context.Context
	config               Config
	stage                string
	input                textinput.Model
	viewport             viewport.Model
	width, height, frame int
	entries              []entry
	provider             llm.Preset
	settings             llm.Config
	models               []string
	selection            int
	modelClient          *llm.Client
	commander            *bridge.Client
	engine               *chat.Engine
	busy                 bool
	activity             string
	operations           map[string]bridge.Operation
	streamIndex          int
	generation           int
	setupCancel          context.CancelFunc
	manual               bool
	forceBottom          bool
	renderer             *lipgloss.Renderer
	controls             int
}

func New(ctx context.Context, config Config, output io.Writer) *Model {
	renderer := lipgloss.NewRenderer(output)
	profile := termenv.TrueColor
	if config.NoColor {
		profile = termenv.Ascii
	}
	renderer.SetColorProfile(profile)
	// Bubble Tea detects the background before taking over stdin. Reuse that
	// result so this renderer never competes with the keyboard reader for replies.
	renderer.SetHasDarkBackground(lipgloss.HasDarkBackground())
	input := textinput.New()
	input.Prompt = "› "
	input.CharLimit = 16000
	input.Width = 74
	input.Focus()
	m := &Model{ctx: ctx, config: config, stage: "provider", input: input, viewport: viewport.New(78, 16), width: 80, height: 28, operations: map[string]bridge.Operation{}, streamIndex: -1, renderer: renderer}
	m.styleInput()
	m.setStage("provider", "/help, /back, or /exit", "")
	return m
}
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, nextTick()}
	if m.config.Provider != "" {
		cmds = append(cmds, func() tea.Msg { return initialProvider(m.config.Provider) })
	}
	return tea.Batch(cmds...)
}

type initialProvider string

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
	switch msg := message.(type) {
	case initialProvider:
		return m, m.selectProvider(string(msg))
	case tick:
		m.frame++
		return m, nextTick()
	case tea.WindowSizeMsg:
		m.width, m.height = max(24, msg.Width), max(12, msg.Height)
		m.input.Width = max(12, m.width-8)
		m.refresh()
		return m, nil
	case modelsResult:
		if msg.generation != m.generation {
			return m, nil
		}
		m.busy = false
		if msg.err != nil || len(msg.names) == 0 {
			text := "No models found. Enter an API base URL and model name manually."
			if msg.err != nil {
				text = "Model discovery failed: " + msg.err.Error() + ". Enter a base URL and model name, or /back."
			}
			m.add("error", text)
			m.manual = true
			m.setStage("url", "API base URL", m.settings.BaseURL)
		} else {
			m.models = msg.names
			m.setStage("model", "/help, /back, or /exit", "")
		}
		return m, nil
	case connected:
		if msg.generation != m.generation {
			if msg.client != nil {
				_ = msg.client.Close()
			}
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.add("error", msg.err.Error()+". Start docker compose, then enter the MCP URL to retry.")
			m.setStage("mcp", "MCP URL", m.config.MCPURL)
			return m, nil
		}
		m.commander = msg.client
		m.engine = chat.New(m.ctx, m.modelClient, m.commander)
		m.setStage("chat", "Ask about missions, agents, or an objective…", "")
		m.add("success", m.provider.Name+" · "+m.settings.Model)
		m.add("success", "Commander connected · "+m.config.MCPURL)
		m.add("notice", "Try: List missions and available agents. Type /help for commands.")
		return m, waitEvent(m.engine)
	case eventMessage:
		if msg.closed {
			return m, tea.Quit
		}
		m.receive(msg.event)
		return m, waitEvent(m.engine)
	case tea.KeyMsg:
		if m.selecting() && m.navigate(msg) {
			return m, nil
		}
		switch msg.String() {
		case "ctrl+d":
			return m, tea.Quit
		case "ctrl+c":
			if m.stage != "chat" {
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
			if value == "/help" && m.stage != "chat" {
				m.input.Reset()
				m.add("notice", "Use ↑/↓ and Enter to select a provider and model; PgUp/PgDown scroll longer lists. Choose Enter model name… for an unlisted model. Other supports OpenAI-compatible APIs. /back restarts setup; /exit quits. Override MCP with --mcp-url.")
				return m, nil
			}
			if value == "/back" && m.stage != "chat" {
				if m.setupCancel != nil {
					m.setupCancel()
				}
				m.generation++
				m.busy = false
				m.manual = false
				m.setStage("provider", "/help, /back, or /exit", "")
				return m, nil
			}
			if m.busy && m.stage != "chat" {
				return m, nil
			}
			if m.selecting() && value == "" {
				return m, m.selectChoice()
			}
			m.input.Reset()
			return m, m.submit(value)
		case "pgup", "pgdown", "ctrl+u":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(message)
	return m, cmd
}

func waitEvent(engine *chat.Engine) tea.Cmd {
	return func() tea.Msg { event, ok := <-engine.Events(); return eventMessage{event, !ok} }
}

func (m *Model) selectProvider(value string) tea.Cmd {
	for _, preset := range llm.Presets() {
		if !strings.EqualFold(value, preset.ID) && !strings.EqualFold(value, preset.Name) {
			continue
		}
		m.provider = preset
		m.settings = llm.Config{Provider: preset.ID, BaseURL: preset.URL, Model: m.config.Model, APIKey: m.config.Keys[preset.ID], Debug: m.config.Debug}
		if m.config.BaseURL != "" {
			m.settings.BaseURL = m.config.BaseURL
		}
		m.manual = false
		if m.settings.BaseURL == "" {
			m.setStage("url", "API base URL (for example http://localhost:1234/v1)", "")
			return nil
		}
		return m.needKey()
	}
	m.add("error", "Choose a provider with ↑/↓ and Enter.")
	return nil
}
func (m *Model) needKey() tea.Cmd {
	if m.settings.APIKey == "" {
		placeholder := "API key (hidden; held only for this session)"
		if m.provider.ID == "other" {
			placeholder = "API key (optional; Enter skips for a local endpoint)"
		}
		m.setStage("key", placeholder, "")
		m.input.EchoMode = textinput.EchoPassword
		return nil
	}
	return m.chooseModel()
}
func (m *Model) chooseModel() tea.Cmd {
	client, err := llm.New(m.settings)
	if err != nil {
		m.add("error", err.Error())
		m.setStage("url", "API base URL", m.settings.BaseURL)
		return nil
	}
	m.modelClient = client
	if m.settings.Model != "" {
		return m.connect()
	}
	if m.manual {
		m.setStage("manual_model", "Exact model name", "")
		return nil
	}
	m.setStage("loading_models", "/help or /exit", "")
	m.busy = true
	m.activity = "Finding models"
	ctx, generation := m.setupContext()
	return func() tea.Msg { names, err := client.Models(ctx); return modelsResult{generation, names, err} }
}
func (m *Model) connect() tea.Cmd {
	client, err := llm.New(m.settings)
	if err != nil {
		m.add("error", err.Error())
		return nil
	}
	m.modelClient = client
	m.setStage("connecting", "/help or /exit", "")
	m.busy = true
	m.activity = "Connecting to Commander"
	ctx, generation := m.setupContext()
	endpoint := m.config.MCPURL
	return func() tea.Msg {
		client, err := bridge.Connect(ctx, endpoint)
		return connected{generation, client, err}
	}
}
func (m *Model) setupContext() (context.Context, int) {
	if m.setupCancel != nil {
		m.setupCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.setupCancel = cancel
	m.generation++
	return ctx, m.generation
}
func (m *Model) setStage(stage, placeholder, value string) {
	m.stage = stage
	m.selection = 0
	if stage == "provider" {
		for i, preset := range llm.Presets() {
			if m.config.Keys[preset.ID] != "" {
				m.selection = i
				break
			}
		}
	}
	m.input.EchoMode = textinput.EchoNormal
	m.input.Placeholder = placeholder
	m.input.SetValue(value)
	m.input.CursorEnd()
}
func (m *Model) submit(value string) tea.Cmd {
	switch m.stage {
	case "provider", "model":
		m.add("error", "Unknown setup command. Use /help, /back, or /exit.")
	case "url":
		if err := llm.ValidateURL(value); err != nil {
			m.add("error", err.Error())
			return nil
		}
		m.settings.BaseURL = value
		return m.needKey()
	case "key":
		if value == "" && m.provider.ID != "other" {
			m.add("error", "An API key is required for this provider.")
			return nil
		}
		m.settings.APIKey = value
		m.input.EchoMode = textinput.EchoNormal
		return m.chooseModel()
	case "manual_model":
		if value == "" {
			m.add("error", "Enter a model name.")
			return nil
		}
		m.settings.Model = value
		return m.connect()
	case "mcp":
		if err := llm.ValidateURL(value); err != nil {
			m.add("error", err.Error())
			return nil
		}
		m.config.MCPURL = value
		return m.connect()
	case "chat":
		if value == "" {
			return nil
		}
		m.add("user", value)
		if err := m.engine.Send(value); err != nil {
			m.add("error", err.Error())
		}
	}
	return nil
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
		m.entries[m.streamIndex].text += clean(event.Text)
		m.refresh()
	case "operation":
		if event.Operation != nil {
			m.operations[event.ID] = *event.Operation
			m.refresh()
		}
	default:
		m.add(event.Kind, event.Text)
	}
}
func (m *Model) add(kind, text string) {
	m.entries = append(m.entries, entry{kind, clean(text)})
	if len(m.entries) > 500 {
		m.entries = m.entries[len(m.entries)-500:]
		m.streamIndex = -1
	}
	m.forceBottom = true
	m.refresh()
}
func clean(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 32 && r != 127 && !(r >= 0x80 && r <= 0x9f)) {
			return r
		}
		return -1
	}, text)
}
