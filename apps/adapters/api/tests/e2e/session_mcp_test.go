package e2e_test

import (
	"encoding/json"
	"syscall"
	"testing"
	"time"

	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func TestE2ESessionMCPDiscovery(t *testing.T) {
	s := e2efixtures.NewSessionStack(t, e2efixtures.ImmediateSessionPeer(t).URL)
	mcp := s.MCP()
	var catalog struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(mcp.RPC("tools/list", map[string]any{}, 0), &catalog); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range catalog.Tools {
		if tool.Name == "list_runtime_sessions" {
			found = true
			if len(tool.InputSchema.Required) != 0 {
				t.Fatalf("session discovery requires prior identifiers: %+v", tool)
			}
		}
	}
	if !found {
		t.Fatal("session discovery absent from MCP tools")
	}
	if got := mcp.List(map[string]any{}); len(got) != 0 {
		t.Fatalf("fresh stack has sessions: %+v", got)
	}
	for _, arguments := range []map[string]any{
		{"scope_id": "not-a-uuid"}, {"scope_id": ""}, {"scope_id": 7},
		{"owner_agent_id": "not-an-agent"}, {"owner_agent_id": ""}, {"owner_agent_id": 7},
		{"scope_id": nil}, {"owner_agent_id": nil},
		{"unexpected": true},
	} {
		mcp.RPC("tools/call", map[string]any{"name": "list_runtime_sessions", "arguments": arguments}, -32602)
	}

	s.Worker(1)
	s.Mission("mcp-discovery", false, "connect-resource")
	firstScope, secondScope := id.New(), id.New()
	first := createSession(t, s, s.Start("mcp-discovery"), firstScope)
	second := createSession(t, s, s.Start("mcp-discovery"), secondScope)
	assertSessions := func(arguments map[string]any, want ...sessions.Session) []sessions.Session {
		t.Helper()
		got := mcp.List(arguments)
		if len(got) != len(want) {
			t.Fatalf("filters %v: got %d sessions, want %d: %+v", arguments, len(got), len(want), got)
		}
		for _, expected := range want {
			found := false
			for _, actual := range got {
				if actual.Reference == expected.Reference {
					found = true
					if actual.Kind != "fixture" || actual.Label != "Owned E2E resource" || actual.State == "" {
						t.Fatalf("session lost discovery metadata: %+v", actual)
					}
				}
			}
			if !found {
				t.Fatalf("filters %v omitted session %+v: %+v", arguments, expected.Reference, got)
			}
		}
		return got
	}
	assertSessions(map[string]any{}, first, second)
	assertSessions(map[string]any{"scope_id": firstScope}, first)
	assertSessions(map[string]any{"owner_agent_id": first.OwnerAgentID}, first, second)

	if err := s.SignalWorker(1, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	e2efixtures.WaitUntil(t, 12*time.Second, func() bool {
		items := mcp.List(map[string]any{"owner_agent_id": first.OwnerAgentID})
		return len(items) == 2 && items[0].State == "lost" && items[1].State == "lost"
	})
	s.Worker(2)
	thirdScope := id.New()
	third := createSession(t, s, s.Start("mcp-discovery"), thirdScope)
	if third.OwnerAgentID == first.OwnerAgentID {
		t.Fatal("new resource assigned to unavailable owner")
	}
	assertSessions(map[string]any{}, first, second, third)
	assertSessions(map[string]any{"owner_agent_id": third.OwnerAgentID}, third)
	assertSessions(map[string]any{"scope_id": firstScope, "owner_agent_id": first.OwnerAgentID}, first)
	assertSessions(map[string]any{"scope_id": firstScope, "owner_agent_id": third.OwnerAgentID})
	assertSessions(map[string]any{"scope_id": id.New()})
	assertSessions(map[string]any{"owner_agent_id": "fixture-agent-ffffffff"})

	for _, control := range []struct {
		session sessions.Session
		method  string
	}{{first, "abandon"}, {third, "close"}} {
		var result sessions.Session
		mcp.Call("control_runtime_session", map[string]any{
			"id": control.session.ID, "scope_id": control.session.ScopeID,
			"owner_agent_id": control.session.OwnerAgentID, "method": control.method, "request_id": id.New(),
		}, &result)
	}
	// Saved discovery remains available without a controller. Lost/abandoned
	// history must not vanish or become connected when inventory is unavailable.
	s.StopController()
	for _, item := range assertSessions(map[string]any{}, first, second, third) {
		switch item.ID {
		case first.ID:
			if item.State != "lost" || item.ReleaseReason != "abandoned" {
				t.Fatalf("abandoned session history changed: %+v", item)
			}
		case second.ID:
			if item.State != "lost" || item.ReleaseReason != "" {
				t.Fatalf("owner loss released its session: %+v", item)
			}
		case third.ID:
			if item.State != "closed" || item.ReleaseReason != "closed" {
				t.Fatalf("closed session history changed: %+v", item)
			}
		}
	}
}
