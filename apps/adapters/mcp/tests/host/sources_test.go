package mcp_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/agents"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPAgentSources(t *testing.T) {
	store, entries := hostfixtures.AgentSources(t)
	ctx, session := hostfixtures.Session(t, adapter.Config{AgentSourceStore: store})
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "get_agent_catalog" {
			t.Fatal("obsolete agent catalog tool must not remain an alias")
		}
		found = found || tool.Name == "get_agent_sources"
	}
	if !found {
		t.Fatal("agent sources tool is missing")
	}
	var listing struct {
		Sources []agents.AgentSource `json:"sources"`
	}

	hostfixtures.MissionCall(t, ctx, session, "get_agent_sources", map[string]any{}, &listing)

	want := []agents.AgentSource{entries[4], entries[3], entries[0], entries[1], entries[2]}
	if !reflect.DeepEqual(listing.Sources, want) {
		t.Fatalf("sources lost public-first/name/source order or saved metadata: %+v", listing)
	}

	revoked := entries[4]
	revoked.Trusted = false
	revoked.Version++
	revoked.UpdatedAt = revoked.UpdatedAt.Add(time.Minute)
	revoked.TrustUpdatedAt = revoked.UpdatedAt
	revoked.TrustUpdatedBy = "revoking-operator"
	if err := store.Replace(ctx, revoked, 1); err != nil {
		t.Fatal(err)
	}

	hostfixtures.MissionCall(t, ctx, session, "get_agent_sources", map[string]any{}, &listing)

	want[0] = revoked
	if !reflect.DeepEqual(listing.Sources, want) {
		t.Fatalf("sources cached stale trust/version metadata: %+v", listing)
	}
}

func TestMCPAgentSourcesEmpty(t *testing.T) {
	ctx, session := hostfixtures.Session(t, adapter.Config{})
	var listing struct {
		Sources []agents.AgentSource `json:"sources"`
	}

	hostfixtures.MissionCall(t, ctx, session, "get_agent_sources", map[string]any{}, &listing)

	if listing.Sources == nil || len(listing.Sources) != 0 {
		t.Fatalf("empty sources must be an empty array: %+v", listing)
	}
}

func TestMCPAgentSourcesFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "backend", err: errors.New("mongodb://user:source-secret@localhost/database"), want: "agent sources read failed"},
		{name: "timeout", err: context.DeadlineExceeded, want: "agent sources read timed out"},
		{name: "canceled", err: context.Canceled, want: "agent sources read canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := fakes.NewAgentSourceReadFailure(test.err)
			ctx, session := hostfixtures.Session(t, adapter.Config{AgentSourceStore: store})
			if reads, _ := store.Reads(); reads != 0 {
				t.Fatal("server initialization queried the sources")
			}

			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_agent_sources", Arguments: map[string]any{}})

			if err != nil || result == nil || !result.IsError || len(result.Content) != 1 {
				t.Fatalf("sources failure must remain a tool error: %+v, %v", result, err)
			}
			message, ok := result.Content[0].(*sdkmcp.TextContent)
			if !ok || !strings.Contains(message.Text, test.want) || strings.Contains(message.Text, "source-secret") {
				t.Fatalf("sources error lost safe failure detail: %+v", result.Content)
			}
			if reads, remaining := store.Reads(); reads != 1 || remaining <= 0 || remaining > 10*time.Second {
				t.Fatalf("sources read must be bounded and not retried: reads=%d, remaining=%s", reads, remaining)
			}

			var running map[string]any
			hostfixtures.MissionCall(t, ctx, session, "list_agents", map[string]any{}, &running)

			if reads, _ := store.Reads(); reads != 1 {
				t.Fatal("unrelated tool read the sources")
			}
		})
	}
}

func TestMCPAgentSourcesRejectsArguments(t *testing.T) {
	store := fakes.NewAgentSourceReadFailure(errors.New("should not read sources"))
	ctx, session := hostfixtures.Session(t, adapter.Config{AgentSourceStore: store})
	for _, input := range []map[string]any{
		{"url": "https://other.example/agents.yaml"},
		{"token": "credential-sentinel"},
		{"trusted": true},
	} {

		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_agent_sources", Arguments: input})

		if err == nil && !result.IsError {
			t.Fatal("read-only discovery accepted untrusted tool input")
		}
	}
	if reads, _ := store.Reads(); reads != 0 {
		t.Fatal("invalid discovery arguments reached persistence")
	}
}
