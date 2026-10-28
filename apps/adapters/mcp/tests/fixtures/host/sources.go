package host

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/framework/agent/packages"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const AgentSources = `schema_version: 1
agents:
  zeta:
    description: Second public source fixture.
    source: github.com/acme/zeta-agent@stable
    visibility: public
  alpha:
    description: First private source fixture.
    source: github.com/private-org/alpha-agent@v1.2.3
    visibility: private
  gamma:
    description: Second private source fixture.
    source: github.com/private-org/gamma-agent@stable
    visibility: private
  beta:
    description: First public source fixture.
    source: github.com/acme/beta-agent@v1.2.3
    visibility: public
`

const EmptyAgentSources = "schema_version: 1\nagents: {}\n"

type SourceEntry struct {
	Name, Description, Source, Visibility string
}

func ExpectedSources() []SourceEntry {
	return []SourceEntry{
		{"beta", "First public source fixture.", "github.com/acme/beta-agent@v1.2.3", "public"},
		{"zeta", "Second public source fixture.", "github.com/acme/zeta-agent@stable", "public"},
		{"alpha", "First private source fixture.", "github.com/private-org/alpha-agent@v1.2.3", "private"},
		{"gamma", "Second private source fixture.", "github.com/private-org/gamma-agent@stable", "private"},
	}
}

func AgentSourcesSession(t *testing.T, transport http.RoundTripper) (context.Context, *sdkmcp.ClientSession) {
	t.Helper()
	client := packages.NewClient(transport)
	t.Cleanup(client.CloseIdleConnections)
	return Session(t, adapter.Config{SourceClient: client})
}

func SourceResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}
