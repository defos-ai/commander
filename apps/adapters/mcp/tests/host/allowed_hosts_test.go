package host_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
)

func TestMCPAllowedHostsEnvironmentAndFlagPrecedence(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		env      string
		flags    []string
		accepted []string
		rejected string
	}{
		{
			name:     "loopback default",
			accepted: []string{"localhost", "127.0.0.1", "[::1]:4273"},
			rejected: "unlisted.example",
		},
		{
			name:     "environment",
			env:      " MCP.Example., 192.0.2.42, ::1 ",
			accepted: []string{"mcp.example:443", "192.0.2.42", "[::1]:4273"},
			rejected: "localhost",
		},
		{
			name:     "explicit flag overrides environment",
			env:      "environment.example",
			flags:    []string{"--allowed-hosts=flag.example"},
			accepted: []string{"flag.example"},
			rejected: "environment.example",
		},
		{
			name:     "explicit default overrides environment",
			env:      "environment.example",
			flags:    []string{"--allowed-hosts=localhost,127.0.0.1,::1"},
			accepted: []string{"127.0.0.1"},
			rejected: "environment.example",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			args := fixtures.HTTPHostArgs(t)
			t.Setenv("MCP_ALLOWED_HOSTS", scenario.env)
			args = append(args, scenario.flags...)

			server := fixtures.StartHTTPHost(t, args)

			for _, hostname := range scenario.accepted {
				assertMCPHostStatus(t, server, hostname, http.StatusOK, http.StatusUnauthorized)
			}
			assertMCPHostStatus(t, server, scenario.rejected, http.StatusForbidden, http.StatusForbidden)
		})
	}
}

func TestMCPAllowedHostsRejectInvalidStartupConfiguration(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		env   string
		flags []string
	}{
		{name: "URL in environment", env: "https://mcp.example"},
		{name: "empty environment list entry", env: "mcp.example,"},
		{name: "explicit empty flag", env: "mcp.example", flags: []string{"--allowed-hosts="}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			args := fixtures.HTTPHostArgs(t)
			t.Setenv("MCP_ALLOWED_HOSTS", scenario.env)
			args = append(args, scenario.flags...)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := cli.Run(ctx, args, io.Discard)

			if err == nil || !strings.Contains(err.Error(), "allowed-hosts") {
				t.Fatalf("expected allowed-hosts validation before startup, got %v", err)
			}
		})
	}
}

func assertMCPHostStatus(t *testing.T, server *fixtures.HTTPHost, hostname string, aboutStatus, mcpStatus int) {
	t.Helper()
	for _, request := range []struct {
		path string
		want int
	}{
		{path: "/aboutz", want: aboutStatus},
		{path: "/mcp", want: mcpStatus},
	} {
		if got := server.Status(t, request.path, hostname); got != request.want {
			t.Errorf("GET %s with Host %s returned %d, want %d", request.path, hostname, got, request.want)
		}
	}
}
