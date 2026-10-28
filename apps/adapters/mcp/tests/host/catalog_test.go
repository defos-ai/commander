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

func TestMCPAgentCatalog(t *testing.T) {
	store, entries := hostfixtures.AgentCatalog(t)
	ctx, session := hostfixtures.Session(t, adapter.Config{CatalogStore: store})
	var listing struct {
		Agents []agents.CatalogEntry `json:"agents"`
	}

	hostfixtures.MissionCall(t, ctx, session, "get_agent_catalog", map[string]any{}, &listing)

	want := []agents.CatalogEntry{entries[4], entries[3], entries[0], entries[1], entries[2]}
	if !reflect.DeepEqual(listing.Agents, want) {
		t.Fatalf("catalog lost public-first/name/package order or saved metadata: %+v", listing)
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

	hostfixtures.MissionCall(t, ctx, session, "get_agent_catalog", map[string]any{}, &listing)

	want[0] = revoked
	if !reflect.DeepEqual(listing.Agents, want) {
		t.Fatalf("catalog cached stale trust/version metadata: %+v", listing)
	}
}

func TestMCPAgentCatalogEmpty(t *testing.T) {
	ctx, session := hostfixtures.Session(t, adapter.Config{})
	var listing struct {
		Agents []agents.CatalogEntry `json:"agents"`
	}

	hostfixtures.MissionCall(t, ctx, session, "get_agent_catalog", map[string]any{}, &listing)

	if listing.Agents == nil || len(listing.Agents) != 0 {
		t.Fatalf("empty catalog must be an empty array: %+v", listing)
	}
}

func TestMCPAgentCatalogFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "backend", err: errors.New("mongodb://user:catalog-secret@localhost/database"), want: "agent catalog read failed"},
		{name: "timeout", err: context.DeadlineExceeded, want: "agent catalog read timed out"},
		{name: "canceled", err: context.Canceled, want: "agent catalog read canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := fakes.NewCatalogReadFailure(test.err)
			ctx, session := hostfixtures.Session(t, adapter.Config{CatalogStore: store})
			if reads, _ := store.Reads(); reads != 0 {
				t.Fatal("server initialization queried the catalog")
			}

			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_agent_catalog", Arguments: map[string]any{}})

			if err != nil || result == nil || !result.IsError || len(result.Content) != 1 {
				t.Fatalf("catalog failure must remain a tool error: %+v, %v", result, err)
			}
			message, ok := result.Content[0].(*sdkmcp.TextContent)
			if !ok || !strings.Contains(message.Text, test.want) || strings.Contains(message.Text, "catalog-secret") {
				t.Fatalf("catalog error lost safe failure detail: %+v", result.Content)
			}
			if reads, remaining := store.Reads(); reads != 1 || remaining <= 0 || remaining > 10*time.Second {
				t.Fatalf("catalog read must be bounded and not retried: reads=%d, remaining=%s", reads, remaining)
			}

			var running map[string]any
			hostfixtures.MissionCall(t, ctx, session, "list_agents", map[string]any{}, &running)

			if reads, _ := store.Reads(); reads != 1 {
				t.Fatal("unrelated tool read the catalog")
			}
		})
	}
}

func TestMCPAgentCatalogRejectsArguments(t *testing.T) {
	store := fakes.NewCatalogReadFailure(errors.New("should not read catalog"))
	ctx, session := hostfixtures.Session(t, adapter.Config{CatalogStore: store})
	for _, input := range []map[string]any{
		{"url": "https://other.example/agents.yaml"},
		{"token": "credential-sentinel"},
		{"trusted": true},
	} {

		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_agent_catalog", Arguments: input})

		if err == nil && !result.IsError {
			t.Fatal("read-only discovery accepted untrusted tool input")
		}
	}
	if reads, _ := store.Reads(); reads != 0 {
		t.Fatal("invalid discovery arguments reached persistence")
	}
}
