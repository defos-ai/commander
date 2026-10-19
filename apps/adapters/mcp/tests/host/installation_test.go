package mcp_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/host"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPInstallationGuidanceWithoutBusinessAgents(t *testing.T) {
	const token = "installation-guidance-test-token"
	endpoint, client, _ := startMCP(t, &host.BearerCredentials{Token: token}, io.Discard)
	client.Transport = bearerTransport{token: token, next: client.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "installation-test", Version: "1"}, nil).Connect(ctx,
		&sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, guidance := range []string{
		"start a separate install-agent operation", "use assign_task for spawn-agent",
		"github.com/defos-ai/commander-web-agent@v0.2.0",
		"github.com/defos-ai/commander-browser-agent@v0.2.0",
		"do not invent a reference", "Recheck list_agents and the target mission's preflight",
	} {
		if !strings.Contains(session.InitializeResult().Instructions, guidance) {
			t.Errorf("MCP instructions omit installation guidance: %s", guidance)
		}
	}
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{}
	for _, tool := range result.Tools {
		available[tool.Name] = true
	}
	for _, name := range []string{"list_missions", "describe_mission", "list_agents", "describe_action", "start_operation", "assign_task", "resolve_continuation", "complete_operation"} {
		if !available[name] {
			t.Errorf("installation needs %s even before business agents register", name)
		}
	}
}
