package playground_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/host"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPBearerAuthentication(t *testing.T) {
	const token = "playground-mcp-test-token"
	var requests, calls, reads atomic.Int32
	var reject atomic.Bool
	server := mcp.NewServer(&mcp.Implementation{Name: "auth-fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "test_action", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"ok":true}`}}}, nil
	})
	server.AddResource(&mcp.Resource{URI: "missions://artifacts/test"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		reads.Add(1)
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "missions://artifacts/test", Text: "artifact evidence"}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if reject.Load() || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "private body "+token, http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer endpoint.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for _, value := range []string{"", "wrong-token"} {
		before := requests.Load()
		_, err := bridge.Connect(ctx, endpoint.URL, host.BearerCredentials{Token: value})
		assertMCPAuthError(t, err, token)
		if requests.Load()-before != 1 {
			t.Error("authentication rejection was retried")
		}
	}
	client, err := bridge.Connect(ctx, endpoint.URL, host.BearerCredentials{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.Call(ctx, "test_action", json.RawMessage(`{}`))
	if err != nil || result.IsError || result.Text != `{"ok":true}` {
		t.Fatalf("authenticated tool call failed: %v", err)
	}
	result, err = client.Call(ctx, "read_artifact", json.RawMessage(`{"uri":"missions://artifacts/test"}`))
	if err != nil || !strings.Contains(result.Text, "artifact evidence") {
		t.Fatalf("authenticated resource read failed: %v", err)
	}
	if calls.Load() != 1 || reads.Load() != 1 {
		t.Fatal("unauthenticated requests reached MCP handlers")
	}
	reject.Store(true)
	before := requests.Load()
	_, err = client.Call(ctx, "test_action", json.RawMessage(`{}`))
	assertMCPAuthError(t, err, token)
	_, err = client.Call(ctx, "read_artifact", json.RawMessage(`{"uri":"missions://artifacts/test"}`))
	assertMCPAuthError(t, err, token)
	if requests.Load()-before != 1 || calls.Load() != 1 || reads.Load() != 1 {
		t.Fatal("rejected session retried or dispatched work")
	}
}

func TestMCPDoesNotForwardTokensOnRedirect(t *testing.T) {
	const token = "private-mcp-token"
	var forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/"+token, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := bridge.Connect(ctx, origin.URL, host.BearerCredentials{Token: token})
	if err == nil || strings.Contains(err.Error(), token) || forwarded.Load() != 0 {
		t.Fatal("MCP followed a redirect or disclosed the token")
	}
}

func TestMCPRejectsInvalidTokenBeforeConnecting(t *testing.T) {
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "unexpected request")
	}))
	defer endpoint.Close()
	const invalid = "private\nheader"
	_, err := bridge.Connect(t.Context(), endpoint.URL, host.BearerCredentials{Token: invalid})
	if err == nil || !strings.Contains(err.Error(), "COMMANDER_MCP_TOKEN") || strings.Contains(err.Error(), invalid) || requests.Load() != 0 {
		t.Fatal("invalid token was sent or disclosed")
	}
}

func assertMCPAuthError(t *testing.T, err error, token string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "authentication failed") || !strings.Contains(err.Error(), "COMMANDER_MCP_TOKEN") || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "private body") {
		t.Fatal("MCP authentication error was missing, unclear, or disclosed response contents")
	}
}

type mcpAuthTransport string

func (token mcpAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+string(token))
	return http.DefaultTransport.RoundTrip(request)
}
