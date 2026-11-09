package fixtures

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/framework/host"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewModelOptimization(t *testing.T, ctx context.Context) (*llm.Client, *bridge.Client, *fakes.ModelOptimization) {
	t.Helper()
	fake := fakes.NewModelOptimization()
	provider := httptest.NewServer(http.HandlerFunc(fake.Model))
	t.Cleanup(provider.Close)
	server := fake.MCP()
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(endpoint.Close)
	commander, err := bridge.Connect(ctx, endpoint.URL, host.BearerCredentials{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = commander.Close() })
	model, err := llm.New(llm.Config{Provider: "openai", Model: "fixture-model", BaseURL: provider.URL})
	if err != nil {
		t.Fatal(err)
	}
	return model, commander, fake
}
