package mcp_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/agent/packages"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPAgentCatalog(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "private-token-sentinel")
	t.Setenv("GH_TOKEN", "private-token-sentinel")
	const source = "https://catalog.example/agents.yaml"
	const catalog = `schema_version: 1
agents:
  zeta:
    description: Private package fixture.
    package: github.com/private-org/private-agent@v1.2.3
    visibility: private
  alpha:
    description: Public package fixture.
    package: github.com/acme/public-agent@stable
    visibility: public
`
	var requests atomic.Int32
	var catalogUnavailable atomic.Bool
	client := packages.NewClient(fakes.CatalogTransport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
			if request.Header.Get(header) != "" {
				t.Errorf("public metadata request included %s", header)
			}
		}
		body, status := "", http.StatusOK
		switch request.URL.String() {
		case source:
			body = catalog
			if catalogUnavailable.Load() {
				status = http.StatusServiceUnavailable
			}
		default:
			t.Errorf("unexpected metadata URL: %s", request.URL)
			return nil, errors.New("unexpected metadata request")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	t.Cleanup(client.CloseIdleConnections)
	ctx, session := hostfixtures.AgentCatalogSession(t, client, source)
	if requests.Load() != 0 {
		t.Fatal("MCP startup or initialization fetched remote agent catalog metadata")
	}
	call := func(name string, args map[string]any, output any) {
		t.Helper()
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s did not return agent catalog metadata", name)
		}
		content, ok := result.Content[0].(*sdkmcp.TextContent)
		if !ok {
			t.Fatalf("%s did not return JSON text", name)
		}
		if err := json.Unmarshal([]byte(content.Text), output); err != nil {
			t.Fatal(err)
		}
	}
	var listing struct {
		Agents []struct {
			Name, Description, Package, Visibility string
		} `json:"agents"`
	}
	call("get_agent_catalog", map[string]any{}, &listing)
	if len(listing.Agents) != 2 || listing.Agents[0].Name != "alpha" || listing.Agents[1].Name != "zeta" ||
		listing.Agents[0].Description != "Public package fixture." || listing.Agents[0].Package != "github.com/acme/public-agent@stable" || listing.Agents[0].Visibility != "public" ||
		listing.Agents[1].Description != "Private package fixture." || listing.Agents[1].Package != "github.com/private-org/private-agent@v1.2.3" || listing.Agents[1].Visibility != "private" {
		t.Fatalf("agent catalog lost sorted entries or visibility: %+v", listing)
	}
	for _, args := range []map[string]any{
		{"url": "https://other.example/agents.yaml"},
		{"token": "private-token-sentinel"},
	} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_agent_catalog", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatal("tool input overrode the catalog URL or accepted a credential")
		}
	}
	if requests.Load() != 1 {
		t.Fatal("untrusted tool input reached metadata transport")
	}
	catalogUnavailable.Store(true)
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_agent_catalog", Arguments: map[string]any{}})
	if err != nil || !result.IsError {
		t.Fatal("unavailable catalog must return a tool error")
	}
	var agents map[string]any
	call("list_agents", map[string]any{}, &agents)
	if requests.Load() != 2 {
		t.Fatal("an unrelated tool fetched agent catalog metadata")
	}
	catalogUnavailable.Store(false)
	call("get_agent_catalog", map[string]any{}, &listing)
	if len(listing.Agents) != 2 || requests.Load() != 3 {
		t.Fatal("catalog discovery did not recover after the source became available")
	}
}
