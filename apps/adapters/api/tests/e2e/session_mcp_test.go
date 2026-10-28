package e2e_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func TestE2ESessionMCPDiscovery(t *testing.T) {
	s := newSessionStack(t, immediateSessionPeer(t).URL)
	mcp := s.mcp()
	var catalog struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(mcp.rpc("tools/list", map[string]any{}, 0), &catalog); err != nil {
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
	if got := mcp.list(map[string]any{}); len(got) != 0 {
		t.Fatalf("fresh stack has sessions: %+v", got)
	}
	for _, arguments := range []map[string]any{
		{"scope_id": "not-a-uuid"}, {"scope_id": ""}, {"scope_id": 7},
		{"owner_agent_id": "not-an-agent"}, {"owner_agent_id": ""}, {"owner_agent_id": 7},
		{"scope_id": nil}, {"owner_agent_id": nil},
		{"unexpected": true},
	} {
		mcp.rpc("tools/call", map[string]any{"name": "list_runtime_sessions", "arguments": arguments}, -32602)
	}

	s.worker(1)
	s.mission("mcp-discovery", false, "connect-resource")
	firstScope, secondScope := id.New(), id.New()
	first := s.create(s.start("mcp-discovery"), firstScope)
	second := s.create(s.start("mcp-discovery"), secondScope)
	assertSessions := func(arguments map[string]any, want ...sessions.Session) []sessions.Session {
		t.Helper()
		got := mcp.list(arguments)
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

	if err := s.processes[1].Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 12*time.Second, func() bool {
		items := mcp.list(map[string]any{"owner_agent_id": first.OwnerAgentID})
		return len(items) == 2 && items[0].State == "lost" && items[1].State == "lost"
	})
	s.worker(2)
	thirdScope := id.New()
	third := s.create(s.start("mcp-discovery"), thirdScope)
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
		mcp.call("control_runtime_session", map[string]any{
			"id": control.session.ID, "scope_id": control.session.ScopeID,
			"owner_agent_id": control.session.OwnerAgentID, "method": control.method, "request_id": id.New(),
		}, &result)
	}
	// Saved discovery remains available without a controller. Lost/abandoned
	// history must not vanish or become connected when inventory is unavailable.
	s.stopController()
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

type sessionMCPClient struct {
	t        *testing.T
	url      string
	token    string
	client   *http.Client
	sequence int
}

func (s *sessionStack) mcp() *sessionMCPClient {
	s.t.Helper()
	root, err := filepath.Abs("../../../../..")
	if err != nil {
		s.t.Fatal(err)
	}
	binary := filepath.Join(s.t.TempDir(), "mcp")
	build := exec.Command("go", "build", "-o", binary, "./apps/adapters/mcp/cmd/commander-mcp")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		s.t.Fatalf("build mcp: %v\n%s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	token := id.New()
	env := append(append([]string{}, s.env...), "MCP_TOKEN="+token)
	startE2E(s.t, binary, []string{"--listen-address=" + address}, env)
	client := &sessionMCPClient{t: s.t, url: "http://" + address, token: token, client: s.client}
	waitUntil(s.t, 10*time.Second, func() bool {
		response, err := s.client.Get(client.url + "/healthz")
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
	client.rpc("initialize", map[string]any{
		"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "session-e2e", "version": "1"},
	}, 0)
	return client
}

func (c *sessionMCPClient) rpc(method string, params any, wantError int) json.RawMessage {
	c.t.Helper()
	c.sequence++
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.sequence, "method": method, "params": params})
	if err != nil {
		c.t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(c.t.Context(), "POST", c.url+"/mcp", bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	response, err := c.client.Do(request)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		c.t.Fatalf("MCP %s: HTTP %d", method, response.StatusCode)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		c.t.Fatal(err)
	}
	if envelope.Error.Code != wantError {
		c.t.Fatalf("MCP %s: error %+v, want code %d", method, envelope.Error, wantError)
	}
	return envelope.Result
}

func (c *sessionMCPClient) call(name string, arguments, out any) {
	c.t.Helper()
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(c.rpc("tools/call", map[string]any{"name": name, "arguments": arguments}, 0), &result); err != nil {
		c.t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" {
		c.t.Fatalf("MCP %s: %+v", name, result)
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), out); err != nil {
		c.t.Fatal(err)
	}
}

func (c *sessionMCPClient) list(arguments map[string]any) []sessions.Session {
	c.t.Helper()
	var result struct {
		Sessions []sessions.Session `json:"sessions"`
	}
	c.call("list_runtime_sessions", arguments, &result)
	if result.Sessions == nil {
		c.t.Fatal("session listing must return an array, not null")
	}
	return result.Sessions
}
