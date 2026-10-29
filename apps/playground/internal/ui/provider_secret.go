package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/admin/client"
	adminsecrets "github.com/defos-ai/commander/apps/admin/secrets"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

type providerSecretResult struct {
	name     string
	existing bool
	err      error
}

func (m *Model) providerSecretName() string {
	if len(m.provider.KeyEnv) == 0 {
		return ""
	}
	return m.provider.KeyEnv[0]
}

func (m *Model) shareProviderSecret() tea.Cmd {
	name := m.providerSecretName()
	if name == "" || m.settings.APIKey == "" || m.providerSecretCancel != nil {
		return nil
	}
	if m.config.API.URL == "" || m.config.API.Credentials.Password == "" {
		where := " is session-only."
		if m.config.SaveProviderKey != nil {
			where = " is available to this Playground."
		}
		m.add("notice", name+where+" Use /admin secrets set "+name+" to share it with agents.")
		return nil
	}
	connection := client.Connection{URL: m.config.API.URL, Credentials: m.config.API.Credentials}
	value := m.settings.APIKey
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	done := make(chan struct{})
	results := make(chan providerSecretResult, 1)
	m.providerSecretCancel, m.providerSecretDone = cancel, done
	go func() {
		defer close(done)
		defer cancel()
		api, err := client.New(connection)
		if err != nil {
			results <- providerSecretResult{name: name, err: err}
			return
		}
		defer api.Close()
		key := secrets.Key{Name: name}
		metadata, err := adminsecrets.Get(ctx, api, key)
		if err == nil && metadata == nil {
			_, err = adminsecrets.Save(ctx, api, key, value, nil)
		}
		results <- providerSecretResult{name: name, existing: metadata != nil, err: err}
	}()
	return func() tea.Msg { return <-results }
}

func (m *Model) providerSecretResult(result providerSecretResult) {
	switch {
	case result.err != nil:
		m.add("notice", result.name+" was not confirmed saved for agents: "+result.err.Error()+". Check Admin Secrets before trying again.")
	case result.existing:
		m.add("notice", result.name+" is already saved; existing value kept.")
	default:
		m.add("notice", result.name+" saved for agents.")
	}
}
