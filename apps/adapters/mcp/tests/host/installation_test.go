package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/host"
	agentfs "github.com/defos-ai/commander/providers/fs/agents"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPAgentGuidanceDiscovery(t *testing.T) {
	database := t.TempDir()
	store, err := agentfs.NewAgentFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{
			ID: "installer-agent-12345678", Type: "installer-agent", Status: agents.AgentReady,
			Responsibilities: []string{"Provision the operator's selected package, then inspect its readiness."},
		},
		ObservedAt: now, LastSeen: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Hour),
	}
	if err := store.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	const token = "installation-guidance-test-token"
	endpoint, client, _ := hostfixtures.StartWithOptions(t, []string{"--database-url=" + database},
		cli.Options{Credentials: &host.BearerCredentials{Token: token}}, io.Discard)
	client.Transport = fakes.BearerTransport{Token: token, Next: client.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "installation-test", Version: "1"}, nil).Connect(ctx,
		&sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, guidance := range []string{
		"Check list_agents and action readiness", "responsibilities with describe_agent",
		"action schemas with describe_action", "follow the mission and agent guidance",
	} {
		if !strings.Contains(session.InitializeResult().Instructions, guidance) {
			t.Errorf("MCP instructions omit discovery guidance: %s", guidance)
		}
	}
	for _, specific := range []string{"install-agent", "spawn-agent", "github.com/defos-ai/commander-"} {
		if strings.Contains(session.InitializeResult().Instructions, specific) {
			t.Errorf("MCP initialization contains agent-specific installation guidance: %s", specific)
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
	for _, name := range []string{"list_missions", "describe_mission", "create_mission", "list_agents", "describe_agent", "describe_action", "get_agent_sources", "start_operation", "assign_task", "resolve_continuation", "complete_operation"} {
		if !available[name] {
			t.Errorf("installation needs %s even before business agents register", name)
		}
	}
	detail, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "describe_agent", Arguments: map[string]any{"agent_id": record.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detail.IsError || len(detail.Content) != 1 {
		t.Fatal("describe_agent did not return saved agent guidance")
	}
	content, ok := detail.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatal("describe_agent did not return JSON text")
	}
	var actual agents.Agent
	if err := json.Unmarshal([]byte(content.Text), &actual); err != nil {
		t.Fatal(err)
	}
	if actual.ID != record.ID || !slices.Equal(actual.Responsibilities, record.Responsibilities) {
		t.Fatal("describe_agent did not preserve the registered agent's responsibilities")
	}
}
