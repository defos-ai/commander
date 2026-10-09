package playground_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		if r.URL.Path == "/aboutz" {
			http.NotFound(w, r)
			return
		}
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
		if assertMCPConnectAuthError(t, err).Command != "" {
			t.Error("missing metadata produced a credential command")
		}
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

func TestMCPCredentialHints(t *testing.T) {
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	_, authentication := bridge.Connect(t.Context(), unavailable.URL+"/mcp", host.BearerCredentials{})
	unavailable.Close()
	assertMCPAuthError(t, authentication, "private-hint-token")
	assertMCPConnectAuthError(t, authentication)
	for _, test := range []struct {
		name, runtime, application, command string
	}{
		{"native MCP", "native", "commander-mcp", "./commander-mcp credentials"},
		{"Docker MCP", "docker", "commander-mcp", "docker exec commander-mcp commander-mcp credentials"},
		{"Compose MCP", "docker-compose", "commander-mcp", "docker compose exec mcp commander-mcp credentials"},
		{"native all-in-one", "native", "commander", "/credentials"},
		{"Docker all-in-one", "docker", "commander", "docker exec commander commander credentials mcp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const token = "private-hint-token"
			var calls, metadata atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/prefix/mcp":
					calls.Add(1)
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, "private body "+token)
				case "/prefix/aboutz":
					metadata.Add(1)
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
						t.Error("public metadata request carried credentials or endpoint query")
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"runtime": test.runtime, "application": test.application, "command": "untrusted-server-command"})
				default:
					t.Error("metadata lookup lost the endpoint mount prefix")
					http.NotFound(w, r)
				}
			}))
			defer endpoint.Close()
			_, err := bridge.Connect(t.Context(), endpoint.URL+"/prefix/mcp", host.BearerCredentials{Token: token})
			assertMCPAuthError(t, err, token)
			failure := assertMCPConnectAuthError(t, err)
			if failure.Command != test.command || strings.Contains(err.Error(), "untrusted-server-command") {
				t.Fatal("credential hint was missing or contained server-provided instructions")
			}
			if !errors.Is(err, errors.Unwrap(authentication)) {
				t.Fatal("credential hint lost the authentication error")
			}
			if calls.Load() != 1 || metadata.Load() != 1 {
				t.Fatal("authentication was retried or metadata was requested more than once")
			}
		})
	}
}

func TestMCPCredentialHintOnToolDiscoveryFailure(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "auth-fixture", Version: "1"}, nil)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var rejected, metadata atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/aboutz" {
			metadata.Add(1)
			fmt.Fprint(w, `{"runtime":"native","application":"commander-mcp"}`)
			return
		}
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error("could not read MCP request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var request struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(body, &request) != nil {
				t.Error("invalid MCP request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if request.Method == "tools/list" {
				rejected.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer endpoint.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := bridge.Connect(ctx, endpoint.URL+"/mcp", host.BearerCredentials{})
	assertMCPAuthError(t, err, "private-token")
	failure := assertMCPConnectAuthError(t, err)
	if failure.Command != "./commander-mcp credentials" || rejected.Load() != 1 || metadata.Load() != 1 {
		t.Fatal("tool discovery rejection was retried or did not provide a credential hint")
	}
}

func TestMCPCredentialHintFallback(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"missing", http.StatusNotFound, "private-metadata-error"},
		{"protected", http.StatusUnauthorized, "private-metadata-error"},
		{"invalid JSON", http.StatusOK, `{"runtime":`},
		{"unknown runtime", http.StatusOK, `{"runtime":"private-metadata-error","application":"commander-mcp"}`},
		{"unknown application", http.StatusOK, `{"runtime":"native","application":"private-metadata-error"}`},
		{"missing application", http.StatusOK, `{"runtime":"native"}`},
		{"unsupported combination", http.StatusOK, `{"runtime":"docker-compose","application":"commander"}`},
		{"multiple JSON objects", http.StatusOK, `{"runtime":"native","application":"commander-mcp"} {}`},
		{"oversized", http.StatusOK, `{"runtime":"native","application":"commander-mcp","data":"` + strings.Repeat("x", 4096) + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/aboutz" {
					w.WriteHeader(test.status)
					fmt.Fprint(w, test.body)
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer endpoint.Close()
			_, err := bridge.Connect(t.Context(), endpoint.URL+"/mcp", host.BearerCredentials{})
			assertMCPAuthError(t, err, "private-metadata-error")
			failure := assertMCPConnectAuthError(t, err)
			if failure.Command != "" {
				t.Fatal("invalid metadata changed the existing authentication error")
			}
		})
	}
}

func TestMCPCredentialHintsDoNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		fmt.Fprint(w, `{"runtime":"native","application":"commander-mcp"}`)
	}))
	defer destination.Close()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/aboutz" {
			http.Redirect(w, r, destination.URL+"/private-redirect", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer endpoint.Close()
	_, err := bridge.Connect(t.Context(), endpoint.URL+"/mcp", host.BearerCredentials{Token: "private-token"})
	assertMCPAuthError(t, err, "private-redirect")
	failure := assertMCPConnectAuthError(t, err)
	if redirected.Load() != 0 || failure.Command != "" {
		t.Fatal("metadata lookup followed a redirect")
	}
}

func TestMCPCredentialHintsRespectCancellation(t *testing.T) {
	started := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/aboutz" {
			close(started)
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer endpoint.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := bridge.Connect(ctx, endpoint.URL+"/mcp", host.BearerCredentials{})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata lookup did not start")
	}
	cancel()
	select {
	case err := <-done:
		assertMCPAuthError(t, err, "private-token")
		if assertMCPConnectAuthError(t, err).Command != "" {
			t.Fatal("cancelled metadata lookup produced a command")
		}
	case <-time.After(time.Second):
		t.Fatal("metadata lookup did not stop after cancellation")
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

func assertMCPConnectAuthError(t *testing.T, err error) *bridge.AuthenticationError {
	t.Helper()
	var failure *bridge.AuthenticationError
	if !errors.As(err, &failure) {
		t.Fatal("connection rejection did not return a typed authentication error")
	}
	if failure.Unwrap() == nil || !errors.Is(err, failure.Unwrap()) || err.Error() != failure.Unwrap().Error() {
		t.Fatal("connection rejection changed the ordinary authentication error")
	}
	return failure
}

type mcpAuthTransport string

func (token mcpAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+string(token))
	return http.DefaultTransport.RoundTrip(request)
}
