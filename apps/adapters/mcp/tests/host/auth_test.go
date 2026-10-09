package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/host"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPRequiresBearerOnEveryRequest(t *testing.T) {
	const token = "configured-test-token"
	t.Setenv("MCP_TOKEN", token)
	var logs bytes.Buffer
	endpoint, client, stop := startMCP(t, nil, &logs)
	for _, call := range []struct{ method, body string }{
		{http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`},
		{http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`},
		{http.MethodPost, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`},
		{http.MethodPost, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"start_operation","arguments":{}}}`},
		{http.MethodPost, `{"jsonrpc":"2.0","id":4,"method":"resources/list"}`},
		{http.MethodPost, `{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"missions://artifacts/private"}}`},
		{http.MethodGet, ""}, {http.MethodDelete, ""}, {http.MethodOptions, ""},
	} {
		for _, authorization := range [][]string{
			nil, {"Bearer rejected-test-token"}, {"Basic " + token}, {"Bearer"},
			{"Bearer " + token, "Bearer " + token}, {"Bearer " + token + ", Bearer " + token},
			{"Bearer " + token + " extra"}, {"Bearer\t" + token},
		} {
			request, err := http.NewRequestWithContext(t.Context(), call.method, endpoint+"/mcp", strings.NewReader(call.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("Content-Type", "application/json")
			for _, value := range authorization {
				request.Header.Add("Authorization", value)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("%s requires 401 with Bearer challenge, got %d", call.method, response.StatusCode)
			}
			if strings.Contains(string(body), token) || strings.Contains(string(body), "rejected-test-token") {
				t.Error("authentication response exposed a token")
			}
		}
	}
	for _, test := range []struct {
		path    string
		headers map[string]string
		want    int
	}{
		{path: "/healthz", want: http.StatusOK},
		{path: "/mcp?access_token=" + token, want: http.StatusUnauthorized},
		{path: "/mcp", headers: map[string]string{"Authorization": "Bearer " + token, "Origin": "https://untrusted.example"}, want: http.StatusForbidden},
		{path: "/mcp", headers: map[string]string{"Authorization": "Bearer " + token, "Sec-Fetch-Site": "cross-site"}, want: http.StatusForbidden},
		{path: "/mcp", headers: map[string]string{"Authorization": "Bearer " + token, "Host": "untrusted.example"}, want: http.StatusForbidden},
	} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range test.headers {
			if name == "Host" {
				request.Host = value
			} else {
				request.Header.Set(name, value)
			}
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != test.want {
			t.Errorf("request status = %d, want %d", response.StatusCode, test.want)
		}
	}
	stop()
	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), "rejected-test-token") {
		t.Error("HTTP diagnostics exposed a bearer token")
	}
}

func TestAuthenticatedMCPProtocol(t *testing.T) {
	const token = "embedded-test-token"
	t.Setenv("MCP_TOKEN", "environment-test-token")
	endpoint, client, _ := startMCP(t, &host.BearerCredentials{Token: token}, io.Discard)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer environment-test-token")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("environment token must not bypass an embedded credential override")
	}
	client.Transport = bearerTransport{token: token, next: client.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "auth-test", Version: "1"}, nil).Connect(ctx,
		&sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatalf("authenticated initialization: %v", err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) == 0 {
		t.Fatalf("authenticated tool discovery: %v", err)
	}
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_missions", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("authenticated tool call: %v", err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatal("authenticated tool call did not return saved-state data")
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok || !json.Valid([]byte(content.Text)) {
		t.Fatal("authenticated tool call did not return JSON")
	}
	if _, err := session.ListResources(ctx, nil); err != nil {
		t.Fatalf("authenticated resource discovery: %v", err)
	}
	if _, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "missions://artifacts/unknown"}); err == nil || strings.Contains(err.Error(), "401") {
		t.Fatal("authenticated resource read must reach resource validation")
	}
}

type bearerTransport struct {
	token string
	next  http.RoundTripper
}

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "bEaReR "+transport.token)
	return transport.next.RoundTrip(request)
}
