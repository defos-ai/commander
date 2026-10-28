package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/host"
	actionfs "github.com/defos-ai/commander/providers/fs/actions"
	agentfs "github.com/defos-ai/commander/providers/fs/agents"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPQueriesPreserveSecretDeclarations(t *testing.T) {
	database := t.TempDir()
	actionStore, err := actionfs.NewActionFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	agentStore, err := agentfs.NewAgentFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	action := actions.Action{
		Name: "build-agent", Status: actions.Unknown, RequiredSecrets: []string{"MODEL_API_KEY", "GITHUB_TOKEN"},
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
	}
	if err := actionStore.Upsert(t.Context(), action); err != nil {
		t.Fatal(err)
	}
	advertised := action
	advertised.Status = actions.Ready
	now := time.Now().UTC()
	record := agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{ID: "builder-agent-12345678", Type: "builder-agent",
			Status: agents.AgentReady, Actions: []actions.Action{advertised}},
		ObservedAt: now, LastSeen: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Hour),
	}
	if err := agentStore.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	const token = "secret-declarations-test-token"
	endpoint, client, _ := hostfixtures.StartWithOptions(t, []string{"--database-url=" + database},
		cli.Options{Credentials: &host.BearerCredentials{Token: token}}, io.Discard)
	client.Transport = fakes.BearerTransport{Token: token, Next: client.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "secret-declarations-test", Version: "1"}, nil).Connect(ctx,
		&sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, guidance := range []string{"missing_requirements of kind secret", "Admin UI Secrets to supply the shared secret by name", "Never request secret values in chat, tool inputs or ask_question", "Saving a secret is not readiness"} {
		if !strings.Contains(session.InitializeResult().Instructions, guidance) {
			t.Errorf("MCP instructions omit secret handoff guidance: %s", guidance)
		}
	}
	call := func(name string, args map[string]any, output any) {
		t.Helper()
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s did not return saved-state data", name)
		}
		content, ok := result.Content[0].(*sdkmcp.TextContent)
		if !ok {
			t.Fatalf("%s did not return JSON text", name)
		}
		if err := json.Unmarshal([]byte(content.Text), output); err != nil {
			t.Fatal(err)
		}
	}
	var catalogAction actions.Action
	call("describe_action", map[string]any{"action_name": action.Name}, &catalogAction)
	if !reflect.DeepEqual(catalogAction.RequiredSecrets, action.RequiredSecrets) {
		t.Fatalf("action detail lost secret declarations: %+v", catalogAction)
	}
	var detail agents.Agent
	call("describe_agent", map[string]any{"agent_id": record.ID}, &detail)
	if len(detail.Actions) != 1 ||
		!reflect.DeepEqual(detail.Actions[0].RequiredSecrets, action.RequiredSecrets) {
		t.Fatalf("agent detail lost secret declarations: %+v", detail)
	}
	var listing struct {
		Agents []agents.Agent `json:"agents"`
	}
	call("list_agents", map[string]any{}, &listing)
	if len(listing.Agents) != 1 || len(listing.Agents[0].Actions) != 1 ||
		!reflect.DeepEqual(listing.Agents[0].Actions[0].RequiredSecrets, action.RequiredSecrets) || listing.Agents[0].Actions[0].Status != actions.Ready {
		t.Fatalf("agent summary lost ready-action requirements: %+v", listing)
	}
}
