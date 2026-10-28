package mcp_test

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPAgentSources(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "private-token-sentinel")
	t.Setenv("GH_TOKEN", "private-token-sentinel")
	var requests atomic.Int32
	var unavailable atomic.Bool
	var remaining atomic.Int64
	transport := fakes.SourceTransport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		deadline, _ := request.Context().Deadline()
		remaining.Store(int64(time.Until(deadline)))
		if request.URL.String() != adapter.DefaultAgentSourcesURL {
			t.Error("discovery requested a different source URL")
		}
		for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
			if request.Header.Get(header) != "" {
				t.Errorf("anonymous discovery forwarded %s", header)
			}
		}
		status := http.StatusOK
		if unavailable.Load() {
			status = http.StatusServiceUnavailable
		}
		return hostfixtures.SourceResponse(request, status, hostfixtures.AgentSources), nil
	})
	ctx, session := hostfixtures.AgentSourcesSession(t, transport)
	if requests.Load() != 0 {
		t.Fatal("server initialization fetched agent sources")
	}
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
		Sources []hostfixtures.SourceEntry `json:"sources"`
	}

	hostfixtures.MissionCall(t, ctx, session, "get_agent_sources", map[string]any{}, &listing)

	if !reflect.DeepEqual(listing.Sources, hostfixtures.ExpectedSources()) {
		t.Fatalf("sources lost public-first/name/source ordering or metadata: %+v", listing)
	}
	if requests.Load() != 1 || remaining.Load() <= 0 || time.Duration(remaining.Load()) > 10*time.Second {
		t.Fatal("source discovery was retried or lacked a bounded context")
	}

	unavailable.Store(true)
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_agent_sources", Arguments: map[string]any{}})

	if err != nil || result == nil || !result.IsError {
		t.Fatalf("unavailable discovery must remain a tool error: %+v, %v", result, err)
	}
	var running map[string]any
	hostfixtures.MissionCall(t, ctx, session, "list_agents", map[string]any{}, &running)
	if requests.Load() != 2 {
		t.Fatal("unrelated tool fetched agent sources")
	}

	unavailable.Store(false)
	hostfixtures.MissionCall(t, ctx, session, "get_agent_sources", map[string]any{}, &listing)

	if !reflect.DeepEqual(listing.Sources, hostfixtures.ExpectedSources()) || requests.Load() != 3 {
		t.Fatal("discovery did not recover after the source became available")
	}
}

func TestMCPAgentSourcesEmpty(t *testing.T) {
	transport := fakes.SourceTransport(func(request *http.Request) (*http.Response, error) {
		return hostfixtures.SourceResponse(request, http.StatusOK, hostfixtures.EmptyAgentSources), nil
	})
	ctx, session := hostfixtures.AgentSourcesSession(t, transport)
	var listing struct {
		Sources []hostfixtures.SourceEntry `json:"sources"`
	}

	hostfixtures.MissionCall(t, ctx, session, "get_agent_sources", map[string]any{}, &listing)

	if listing.Sources == nil || len(listing.Sources) != 0 {
		t.Fatalf("empty sources must be an empty array: %+v", listing)
	}
}

func TestMCPAgentSourcesFailures(t *testing.T) {
	for _, test := range []struct {
		name, body string
		err        error
	}{
		{name: "transport", err: errors.New("https://source.example/private?token=source-secret")},
		{name: "invalid YAML", body: "source-secret: ["},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			transport := fakes.SourceTransport(func(request *http.Request) (*http.Response, error) {
				requests.Add(1)
				if test.err != nil {
					return nil, test.err
				}
				return hostfixtures.SourceResponse(request, http.StatusOK, test.body), nil
			})
			ctx, session := hostfixtures.AgentSourcesSession(t, transport)

			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_agent_sources", Arguments: map[string]any{}})

			if err != nil || result == nil || !result.IsError || len(result.Content) != 1 {
				t.Fatalf("source failure must remain a tool error: %+v, %v", result, err)
			}
			message, ok := result.Content[0].(*sdkmcp.TextContent)
			if !ok || message.Text != "agent sources read failed" || strings.Contains(message.Text, "source-secret") || requests.Load() != 1 {
				t.Fatal("source error exposed remote data or retried discovery")
			}
		})
	}
}

func TestMCPAgentSourcesRejectsArguments(t *testing.T) {
	var requests atomic.Int32
	transport := fakes.SourceTransport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return hostfixtures.SourceResponse(request, http.StatusOK, hostfixtures.AgentSources), nil
	})
	ctx, session := hostfixtures.AgentSourcesSession(t, transport)
	for _, input := range []map[string]any{
		{"url": "https://other.example/sources.yaml"},
		{"token": "credential-sentinel"},
		{"trusted": true},
	} {

		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_agent_sources", Arguments: input})

		if err == nil && !result.IsError {
			t.Fatal("read-only discovery accepted untrusted tool input")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid discovery arguments reached the remote source")
	}
}
