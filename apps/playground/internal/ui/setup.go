package ui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/chat"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
)

type setupStage string

const (
	stageProvider      setupStage = "provider"
	stageURL           setupStage = "url"
	stageKey           setupStage = "key"
	stageLoadingModels setupStage = "loading_models"
	stageModel         setupStage = "model"
	stageManualModel   setupStage = "manual_model"
	stageMCP           setupStage = "mcp"
	stageConnecting    setupStage = "connecting"
	stageChat          setupStage = "chat"
)

type setupState struct {
	stage       setupStage
	provider    llm.Preset
	settings    llm.Config
	models      []string
	selection   int
	modelClient *llm.Client
	generation  int
	setupCancel context.CancelFunc
	manual      bool
}

type initialProvider string

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

func (m *Model) setupResult(message tea.Msg) (tea.Cmd, bool) {
	switch msg := message.(type) {
	case initialProvider:
		return m.selectProvider(string(msg)), true
	case modelsResult:
		if msg.generation != m.generation {
			return nil, true
		}
		m.busy = false
		if msg.err != nil || len(msg.names) == 0 {
			text := "No models found. Enter an API base URL and model name manually."
			if msg.err != nil {
				text = "Model discovery failed: " + msg.err.Error() + ". Enter a base URL and model name, or /back."
			}
			m.add("error", text)
			m.manual = true
			m.setStage(stageURL, "API base URL", m.settings.BaseURL)
		} else {
			m.models = msg.names
			m.setStage(stageModel, "/help, /back, or /exit", "")
		}
		return nil, true
	case connected:
		if msg.generation != m.generation {
			if msg.client != nil {
				_ = msg.client.Close()
			}
			return nil, true
		}
		m.busy = false
		if msg.err != nil {
			m.add("error", msg.err.Error()+". Start docker compose, then enter the MCP URL to retry.")
			m.setStage(stageMCP, "MCP URL", m.config.MCPURL)
			return nil, true
		}
		m.commander = msg.client
		m.engine = chat.New(m.ctx, m.modelClient, m.commander)
		m.setStage(stageChat, "Ask about missions, agents, or an objective…", "")
		m.add("success", m.provider.Name+" · "+m.settings.Model)
		m.add("success", "Commander connected · "+m.config.MCPURL)
		for _, notice := range m.config.Notices {
			m.add("notice", notice)
		}
		m.add("notice", "Try: List missions and available agents. Type /help for commands.")
		return waitEvent(m.engine), true
	}
	return nil, false
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
			m.setStage(stageURL, "API base URL (for example http://localhost:1234/v1)", "")
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
		m.setStage(stageKey, placeholder, "")
		m.input.EchoMode = textinput.EchoPassword
		return nil
	}
	return m.chooseModel()
}
func (m *Model) chooseModel() tea.Cmd {
	client, err := llm.New(m.settings)
	if err != nil {
		m.add("error", err.Error())
		m.setStage(stageURL, "API base URL", m.settings.BaseURL)
		return nil
	}
	m.modelClient = client
	if m.settings.Model != "" {
		return m.connect()
	}
	if m.manual {
		m.setStage(stageManualModel, "Exact model name", "")
		return nil
	}
	m.setStage(stageLoadingModels, "/help or /exit", "")
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
	m.setStage(stageConnecting, "/help or /exit", "")
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
func (m *Model) setStage(stage setupStage, placeholder, value string) {
	m.stage = stage
	m.selection = 0
	if stage == stageProvider {
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
	case stageProvider, stageModel:
		m.add("error", "Unknown setup command. Use /help, /back, or /exit.")
	case stageURL:
		if err := llm.ValidateURL(value); err != nil {
			m.add("error", err.Error())
			return nil
		}
		m.settings.BaseURL = value
		return m.needKey()
	case stageKey:
		if value == "" && m.provider.ID != "other" {
			m.add("error", "An API key is required for this provider.")
			return nil
		}
		m.settings.APIKey = value
		m.input.EchoMode = textinput.EchoNormal
		return m.chooseModel()
	case stageManualModel:
		if value == "" {
			m.add("error", "Enter a model name.")
			return nil
		}
		m.settings.Model = value
		return m.connect()
	case stageMCP:
		if err := llm.ValidateURL(value); err != nil {
			m.add("error", err.Error())
			return nil
		}
		m.config.MCPURL = value
		return m.connect()
	}
	return nil
}

func (m *Model) setupInput(value string) tea.Cmd {
	switch value {
	case "/help":
		m.input.Reset()
		m.add("notice", "Use ↑/↓ and Enter to select a provider and model; PgUp/PgDown scroll longer lists. Choose Enter model name… for an unlisted model. Other supports OpenAI-compatible APIs. /back restarts setup; /exit quits. Override MCP with --mcp-url.")
		return nil
	case "/back":
		if m.setupCancel != nil {
			m.setupCancel()
		}
		m.generation++
		m.busy = false
		m.manual = false
		m.setStage(stageProvider, "/help, /back, or /exit", "")
		return nil
	}
	if m.busy {
		return nil
	}
	if m.selecting() && value == "" {
		return m.selectChoice()
	}
	m.input.Reset()
	return m.submit(value)
}
