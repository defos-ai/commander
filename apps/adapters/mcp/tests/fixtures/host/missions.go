package host

import (
	"context"
	"io"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/host"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func AgentCatalogSession(t *testing.T, client *packages.Client, source string) (context.Context, *sdkmcp.ClientSession) {
	t.Helper()
	return Session(t, adapter.Config{CatalogClient: client, CatalogURL: source})
}

func MissionSession(t *testing.T, database string) (context.Context, *sdkmcp.ClientSession, func()) {
	t.Helper()
	const token = "mission-creation-test-token"
	endpoint, client, stop := StartWithOptions(t, []string{"--database-url=" + database},
		cli.Options{Credentials: &host.BearerCredentials{Token: token}}, io.Discard)
	client.Transport = fakes.BearerTransport{Token: token, Next: client.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "mission-test", Version: "1"}, nil).Connect(ctx,
		&sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return ctx, session, stop
}
