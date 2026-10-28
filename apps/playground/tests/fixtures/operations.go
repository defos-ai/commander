package fixtures

import (
	"context"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/framework/host"
)

func AdminOperationUI(t *testing.T, ctx context.Context, adminURL string, noColor bool) (*UIDriver, *fakes.OperationServer) {
	t.Helper()
	operations := fakes.NewOperationServer(t, false)
	endpoint := httptest.NewServer(operations.Handler())
	t.Cleanup(endpoint.Close)
	provider := httptest.NewServer(fakes.AdminReview(t))
	t.Cleanup(provider.Close)
	u := NewUIWithConfig(t, ctx, ui.Config{
		Provider: "other", Model: "fixture", BaseURL: provider.URL, MCP: ui.Connection[host.BearerCredentials]{URL: endpoint.URL},
		Keys: map[string]string{"other": "test-only"}, NoColor: noColor, AdminURL: adminURL,
	})
	return u, operations
}

func SecretFormUI(t *testing.T, ctx context.Context, api ui.Connection[host.BasicCredentials], private ...string) (*UIDriver, *fakes.OperationServer) {
	t.Helper()
	operations := fakes.NewOperationServer(t, true, private...)
	endpoint := httptest.NewServer(operations.Handler())
	t.Cleanup(endpoint.Close)
	provider := httptest.NewServer(fakes.SecretReview(t, private...))
	t.Cleanup(provider.Close)
	u := NewUIWithConfig(t, ctx, ui.Config{Provider: "other", Model: "fixture", BaseURL: provider.URL, MCP: ui.Connection[host.BearerCredentials]{URL: endpoint.URL}, NoColor: true, API: api})
	u.Model.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
	return u, operations
}

func QuestionUI(t *testing.T, ctx context.Context, operation bridge.Operation, questionID string) (*UIDriver, *fakes.QuestionServer) {
	t.Helper()
	questions := fakes.NewQuestionServer(t, operation, questionID)
	endpoint := httptest.NewServer(questions.Handler())
	t.Cleanup(endpoint.Close)
	return NewUI(t, ctx, endpoint.URL, "http://127.0.0.1:1"), questions
}
