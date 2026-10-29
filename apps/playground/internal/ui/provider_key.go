package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/state"
)

type providerKeyState struct {
	attempted map[[2]string]bool
	entered   bool
	loaded    bool
	done      []<-chan struct{}
}

type providerKeyResult struct {
	generation int
	value      string
	err        error
}

type providerKeySaved struct{ err error }
type settingsSaved struct{ err error }

func (m *Model) loadProviderKey() tea.Cmd {
	if m.providerKey.attempted == nil {
		m.providerKey.attempted = make(map[[2]string]bool)
	}
	identity := [2]string{m.provider.ID, m.settings.BaseURL}
	if m.config.LoadProviderKey == nil || m.providerKey.attempted[identity] {
		return nil
	}
	m.providerKey.attempted[identity] = true
	m.setStage(stageLoadingModels, "/help or /exit", "")
	m.busy, m.activity = true, "Loading saved provider key"
	_, generation := m.setupContext()
	load := m.config.LoadProviderKey
	results := make(chan providerKeyResult, 1)
	done := make(chan struct{})
	m.providerKey.done = append(m.providerKey.done, done)
	go func() {
		defer close(done)
		value, err := load(identity[0], identity[1])
		results <- providerKeyResult{generation: generation, value: value, err: err}
	}()
	return func() tea.Msg { return <-results }
}

func (m *Model) saveProviderKey() tea.Cmd {
	if !m.providerKey.entered || m.settings.APIKey == "" || m.config.SaveProviderKey == nil {
		return nil
	}
	m.providerKey.entered = false
	provider, endpoint, value := m.provider.ID, m.settings.BaseURL, m.settings.APIKey
	save := m.config.SaveProviderKey
	results := make(chan providerKeySaved, 1)
	done := make(chan struct{})
	m.providerKey.done = append(m.providerKey.done, done)
	go func() {
		defer close(done)
		results <- providerKeySaved{err: save(provider, endpoint, value)}
	}()
	return func() tea.Msg { return <-results }
}

func (m *Model) saveSettings() tea.Cmd {
	if m.config.SaveSettings == nil {
		return nil
	}
	settings := state.Settings{
		Provider: m.provider.ID, Model: m.settings.Model, BaseURL: m.settings.BaseURL,
		Remote: state.ConnectionSettings{MCPURL: m.config.MCP.URL, APIURL: m.config.API.URL, APIUsername: m.config.API.Credentials.Username},
	}
	save := m.config.SaveSettings
	results := make(chan settingsSaved, 1)
	done := make(chan struct{})
	m.settingsSaveDone = done
	go func() {
		defer close(done)
		results <- settingsSaved{err: save(settings)}
	}()
	return func() tea.Msg { return <-results }
}
