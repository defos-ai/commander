package fixtures

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/framework/host"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ProviderSecretValue = "private-provider-fixture-key"
const ProviderSecretPassword = "private-provider-secret-api-password"
const UnrelatedProviderSecretValue = "private-unrelated-provider-fixture-key"

type ProviderSecretSetupOptions struct {
	API                fakes.ProviderSecretOptions
	EnteredKey         bool
	Provider           string
	MissingAPIURL      bool
	MissingAPIPassword bool
}

func ProviderSecretSetup(t *testing.T, options ProviderSecretSetupOptions) (*UIDriver, *fakes.ProviderSecrets) {
	t.Helper()
	api := fakes.NewProviderSecrets(options.API)
	endpoint := httptest.NewServer(api)
	t.Cleanup(endpoint.Close)
	server := mcp.NewServer(&mcp.Implementation{Name: "provider-secret-fixture", Version: "1"}, nil)
	commander := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(commander.Close)
	provider := options.Provider
	if provider == "" {
		provider = "openai"
	}
	config := ui.Config{
		Provider: provider, Model: "fixture", NoColor: true,
		Keys: map[string]string{},
		MCP:  ui.Connection[host.BearerCredentials]{URL: commander.URL},
		API: ui.Connection[host.BasicCredentials]{URL: endpoint.URL, Credentials: host.BasicCredentials{
			Username: "commander", Password: ProviderSecretPassword,
		}},
	}
	other := "openai"
	if provider == other {
		other = "anthropic"
	}
	config.Keys[other] = UnrelatedProviderSecretValue
	if !options.EnteredKey {
		config.Keys[provider] = ProviderSecretValue
	}
	if provider == "other" {
		config.BaseURL = "http://127.0.0.1:1"
	}
	if options.MissingAPIURL {
		config.API.URL = ""
	}
	if options.MissingAPIPassword {
		config.API.Credentials.Password = ""
	}
	return NewProviderSetup(t, config, io.Discard), api
}
