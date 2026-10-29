package fixtures

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/framework/host"
)

func ProviderKeyUI(t *testing.T, cache *fakes.ProviderKeyCache, provider *fakes.ProviderKeyModel, key string, output io.Writer) (*UIDriver, string) {
	t.Helper()
	endpoint := httptest.NewServer(provider)
	t.Cleanup(endpoint.Close)
	commander := httptest.NewServer(fakes.NewToolStatusCommander("inspect", nil).Handler())
	t.Cleanup(commander.Close)
	baseURL := endpoint.URL + "/custom/v1"
	config := ui.Config{
		Provider: "openai", BaseURL: baseURL, Keys: map[string]string{"openai": key}, NoColor: true,
		MCP:             ui.Connection[host.BearerCredentials]{URL: commander.URL},
		LoadProviderKey: cache.Load, SaveProviderKey: cache.Save,
	}
	return NewProviderSetup(t, config, output), baseURL
}
