package mcp_test

import (
	"fmt"
	"testing"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPSessionDiscoveryRejectsInvalidFiltersBeforeStorage(t *testing.T) {
	for _, args := range []map[string]any{
		{"scope_id": "not-a-uuid"}, {"owner_agent_id": "fixture-agent"},
		{"scope_id": ""}, {"owner_agent_id": ""},
		{"scope_id": nil}, {"owner_agent_id": nil}, {"unknown": "00000000-0000-4000-8000-000000000001"},
	} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			store := fakes.NewSessionQueries()
			ctx, client := hostfixtures.Session(t, adapter.Config{SessionStore: store})

			result, err := client.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_runtime_sessions", Arguments: args})

			if err == nil && !result.IsError {
				t.Fatal("invalid session filters were accepted")
			}
			select {
			case got := <-store.Queries():
				t.Fatalf("invalid filters reached session storage: %+v", got)
			default:
			}
		})
	}
}
