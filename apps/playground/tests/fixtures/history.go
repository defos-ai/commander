package fixtures

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/framework/host"
)

func StartupHistory() ui.History {
	return ui.History{Version: 1,
		Messages: []llm.Message{{Role: "user", Text: "Earlier request"}, {Role: "assistant", Text: "Earlier reply"}},
		Entries: []ui.HistoryEntry{
			{Kind: "success", Text: "OpenAI · chat-latest"},
			{Kind: "success", Text: "Commander connected · http://mcp:4273/mcp"},
			{Kind: "notice", Text: "Try: Install the web agent, then review defos.ai. Type /help for commands."},
			{Kind: "notice", Text: "OPENAI_API_KEY saved for agents."},
			{Kind: "notice", Text: "Saved conversation restored. Use /watch <id> to resume operation review."},
			{Kind: "notice", Text: "OPENAI_API_KEY is already saved; existing value kept."},
			{Kind: "success", Text: "Commander ready · all-in-one · 1 agent connected"},
			{Kind: "user", Text: "Earlier request"},
			{Kind: "assistant", Text: "Earlier reply"},
			{Kind: "tool", Text: "inspect", CallID: "interrupted-call"},
			{Kind: "success", Text: "inspect completed", CallID: "completed-call"},
			{Kind: "notice", Text: "Saved operation available"},
			{Kind: "admin", Text: "Admin state retained"},
			{Kind: "user", Text: "Commander connected · operator note"},
		}}
}

func HistoryUI(t *testing.T, history ui.History) (*UIDriver, *fakes.HistoryStore) {
	t.Helper()
	return historyUI(t, history, nil, true)
}

func RemoteHistoryUI(t *testing.T, history ui.History) (*UIDriver, *fakes.HistoryStore) {
	t.Helper()
	return historyUI(t, history, nil, false)
}

func FailingHistoryUI(t *testing.T) *UIDriver {
	t.Helper()
	u, _ := historyUI(t, ui.History{}, errors.New("fixture history write failed"), true)
	return u
}

func historyUI(t *testing.T, history ui.History, failure error, allInOne bool) (*UIDriver, *fakes.HistoryStore) {
	t.Helper()
	commander := fakes.NewToolStatusCommander("inspect", nil)
	const endpoint = "http://commander.history.test/mcp"
	const provider = "http://provider.history.test/v1"
	previous := http.DefaultTransport
	http.DefaultTransport = fakes.HistoryTransport{
		"commander.history.test": commander.Handler(),
		"provider.history.test":  fakes.NewToolStatusModel(nil, "New reply", "New reply").Handler(),
	}
	t.Cleanup(func() { http.DefaultTransport = previous })
	if history.Provider == "" {
		history.Provider, history.Model, history.BaseURL = "other", "fixture", provider
	}
	store := fakes.NewHistoryStore(history, failure)
	config := ui.Config{
		Provider: "other", Model: "fixture", BaseURL: provider, NoColor: true,
		Keys: map[string]string{"other": "provider-key-must-not-be-saved"},
		MCP: ui.Connection[host.BearerCredentials]{URL: endpoint, Credentials: host.BearerCredentials{
			Token: "mcp-token-must-not-be-saved",
		}},
		API: ui.Connection[host.BasicCredentials]{Credentials: host.BasicCredentials{
			Username: "commander", Password: "api-password-must-not-be-saved",
		}},
		AllInOne: allInOne, LocalAgentCount: 2,
		LoadHistory: store.Load, SaveHistory: store.Save,
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return NewUIWithConfig(t, ctx, config), store
}
