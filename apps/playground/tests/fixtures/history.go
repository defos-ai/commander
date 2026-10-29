package fixtures

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/framework/host"
)

func HistoryUI(t *testing.T, history ui.History) (*UIDriver, *fakes.HistoryStore) {
	t.Helper()
	return historyUI(t, history, nil)
}

func FailingHistoryUI(t *testing.T) *UIDriver {
	t.Helper()
	u, _ := historyUI(t, ui.History{}, errors.New("fixture history write failed"))
	return u
}

func historyUI(t *testing.T, history ui.History, failure error) (*UIDriver, *fakes.HistoryStore) {
	t.Helper()
	commander := fakes.NewToolStatusCommander("inspect", nil)
	endpoint := httptest.NewServer(commander.Handler())
	t.Cleanup(endpoint.Close)
	provider := httptest.NewServer(fakes.NewToolStatusModel(nil, "New reply", "New reply").Handler())
	t.Cleanup(provider.Close)
	if history.Provider == "" {
		history.Provider, history.Model, history.BaseURL = "other", "fixture", provider.URL
	}
	store := fakes.NewHistoryStore(history, failure)
	config := ui.Config{
		Provider: "other", Model: "fixture", BaseURL: provider.URL, NoColor: true,
		Keys: map[string]string{"other": "provider-key-must-not-be-saved"},
		MCP: ui.Connection[host.BearerCredentials]{URL: endpoint.URL, Credentials: host.BearerCredentials{
			Token: "mcp-token-must-not-be-saved",
		}},
		API: ui.Connection[host.BasicCredentials]{Credentials: host.BasicCredentials{
			Username: "commander", Password: "api-password-must-not-be-saved",
		}},
		AllInOne: true, LocalAgentCount: 2,
		LoadHistory: store.Load, SaveHistory: store.Save,
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return NewUIWithConfig(t, ctx, config), store
}
