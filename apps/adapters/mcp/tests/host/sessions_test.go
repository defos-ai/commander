package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/framework/domain/sessions"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPSessionDiscoveryFilters(t *testing.T) {
	const scope = "00000000-0000-4000-8000-000000000001"
	const owner = "fixture-agent-00000001"
	store := sessionQueryStore{queries: make(chan sessions.Query, 1)}
	ctx, client := mcpSession(t, adapter.Config{SessionStore: store})
	for _, tc := range []struct {
		name string
		args map[string]any
		want sessions.Query
	}{
		{"all", map[string]any{}, sessions.Query{}},
		{"scope", map[string]any{"scope_id": scope}, sessions.Query{ScopeID: scope}},
		{"owner", map[string]any{"owner_agent_id": owner}, sessions.Query{OwnerAgentID: owner}},
		{"intersection", map[string]any{"scope_id": scope, "owner_agent_id": owner}, sessions.Query{ScopeID: scope, OwnerAgentID: owner}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := client.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_runtime_sessions", Arguments: tc.args})
			if err != nil || result.IsError || len(result.Content) != 1 {
				t.Fatalf("list sessions: result=%+v error=%v", result, err)
			}
			content, ok := result.Content[0].(*sdkmcp.TextContent)
			if !ok {
				t.Fatal("session discovery did not return JSON text")
			}
			var listing struct {
				Sessions []json.RawMessage `json:"sessions"`
			}
			if err := json.Unmarshal([]byte(content.Text), &listing); err != nil || listing.Sessions == nil || len(listing.Sessions) != 0 {
				t.Fatalf("empty session discovery: %s, error=%v", content.Text, err)
			}
			if got := <-store.queries; got != tc.want {
				t.Fatalf("session filters changed: got=%+v want=%+v", got, tc.want)
			}
		})
	}

	for _, args := range []map[string]any{
		{"scope_id": "not-a-uuid"}, {"owner_agent_id": "fixture-agent"},
		{"scope_id": ""}, {"owner_agent_id": ""},
		{"scope_id": nil}, {"owner_agent_id": nil}, {"unknown": scope},
	} {
		t.Run("invalid "+fmt.Sprint(args), func(t *testing.T) {
			invalidStore := sessionQueryStore{queries: make(chan sessions.Query, 1)}
			ctx, client := mcpSession(t, adapter.Config{SessionStore: invalidStore})
			result, err := client.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_runtime_sessions", Arguments: args})
			if err == nil && !result.IsError {
				t.Fatal("invalid session filters were accepted")
			}
			select {
			case got := <-invalidStore.queries:
				t.Fatalf("invalid filters reached session storage: %+v", got)
			default:
			}
		})
	}
}

type sessionQueryStore struct {
	sessions.Store
	queries chan sessions.Query
}

func (s sessionQueryStore) Find(_ context.Context, query sessions.Query) ([]sessions.Record, error) {
	s.queries <- query
	return []sessions.Record{}, nil
}
