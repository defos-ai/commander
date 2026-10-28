package fixtures

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

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
	u, server, _ := QuestionUIWithOptions(t, ctx, operation, questionID, QuestionUIOptions{})
	return u, server
}

const QuestionOperationID = "846d2dbc-6d3a-42ab-ae42-3d60aecc1f48"
const QuestionID = "a0ca27be-0cac-4881-a3f2-1b8d17afc581"
const QuestionAPIPassword = "private-question-api-password"

type QuestionUIOptions struct {
	API         fakes.QuestionAPIOptions
	NoAPILogin  bool
	APIPassword string
}

func PendingQuestionOperation() bridge.Operation {
	now := time.Now()
	return bridge.Operation{ID: QuestionOperationID, Status: "waiting", UpdatedAt: now,
		Progress: bridge.Progress{TaskCounts: map[string]int{}, PendingQuestions: 1},
		Questions: []bridge.Question{{ID: QuestionID, Status: bridge.QuestionPending,
			Text: "Accept this release?", Options: []string{"Accept", "Decline"}, AskedAt: now, ExpiresAt: now.Add(time.Minute)}},
	}
}

func QuestionUIWithOptions(t *testing.T, ctx context.Context, operation bridge.Operation, questionID string, options QuestionUIOptions) (*UIDriver, *fakes.QuestionServer, string) {
	t.Helper()
	options.API.Password = QuestionAPIPassword
	questions := fakes.NewQuestionServer(t, operation, questionID, options.API)
	endpoint := httptest.NewServer(questions.Handler())
	t.Cleanup(endpoint.Close)
	api := httptest.NewServer(questions.APIHandler())
	t.Cleanup(api.Close)
	connection := ui.Connection[host.BasicCredentials]{}
	if !options.NoAPILogin {
		password := options.APIPassword
		if password == "" {
			password = QuestionAPIPassword
		}
		connection = ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: password}}
	}
	u := NewUIWithConfig(t, ctx, ui.Config{Provider: "other", Model: "fixture", BaseURL: "http://127.0.0.1:1",
		MCP: ui.Connection[host.BearerCredentials]{URL: endpoint.URL}, NoColor: true, API: connection})
	return u, questions, api.URL
}
