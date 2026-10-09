package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
)

func TestMCPAboutDeployment(t *testing.T) {
	t.Setenv("MCP_TOKEN", "metadata-test-token")
	for _, test := range []struct {
		name, environment, application, runtime string
		args                                    []string
	}{
		{name: "native default", runtime: "native"},
		{name: "native environment", environment: "native", runtime: "native"},
		{name: "Docker environment", environment: "docker", runtime: "docker"},
		{name: "Compose environment", environment: "docker-compose", runtime: "docker-compose"},
		{name: "flag overrides environment", environment: "docker", args: []string{"--runtime=native"}, runtime: "native"},
		{name: "embedded Commander", environment: "docker", application: "commander", runtime: "docker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MCP_RUNTIME", test.environment)
			endpoint, client, _ := startMCPWithOptions(t, test.args, cli.Options{Application: test.application}, io.Discard)
			response, err := client.Get(endpoint + "/aboutz")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" ||
				response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("WWW-Authenticate") != "" {
				t.Fatalf("public metadata response: status=%d, headers=%v", response.StatusCode, response.Header)
			}
			var metadata map[string]string
			if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
				t.Fatal(err)
			}
			application := test.application
			if application == "" {
				application = "commander-mcp"
			}
			if len(metadata) != 2 || metadata["runtime"] != test.runtime || metadata["application"] != application {
				t.Fatalf("deployment metadata = %v", metadata)
			}
		})
	}
}

func TestMCPAboutRouteAndOriginProtection(t *testing.T) {
	t.Setenv("MCP_TOKEN", "metadata-test-token")
	t.Setenv("MCP_RUNTIME", "native")
	endpoint, client, _ := startMCP(t, nil, io.Discard)
	for _, test := range []struct {
		name, method, path string
		headers            map[string][]string
		want               int
	}{
		{name: "public GET", method: http.MethodGet, path: "/aboutz", want: http.StatusOK},
		{name: "public HEAD", method: http.MethodHead, path: "/aboutz", want: http.StatusOK},
		{name: "same origin", method: http.MethodGet, path: "/aboutz", headers: map[string][]string{"Origin": {endpoint}}, want: http.StatusOK},
		{name: "untrusted host", method: http.MethodGet, path: "/aboutz", headers: map[string][]string{"Host": {"untrusted.example"}}, want: http.StatusForbidden},
		{name: "cross origin", method: http.MethodGet, path: "/aboutz", headers: map[string][]string{"Origin": {"https://untrusted.example"}}, want: http.StatusForbidden},
		{name: "multiple origins", method: http.MethodGet, path: "/aboutz", headers: map[string][]string{"Origin": {endpoint, endpoint}}, want: http.StatusForbidden},
		{name: "cross site", method: http.MethodHead, path: "/aboutz", headers: map[string][]string{"Sec-Fetch-Site": {"cross-site"}}, want: http.StatusForbidden},
		{name: "no POST", method: http.MethodPost, path: "/aboutz", want: http.StatusMethodNotAllowed},
		{name: "no OPTIONS", method: http.MethodOptions, path: "/aboutz", want: http.StatusMethodNotAllowed},
		{name: "no DELETE", method: http.MethodDelete, path: "/aboutz", want: http.StatusMethodNotAllowed},
		{name: "exact path", method: http.MethodGet, path: "/aboutz/child", want: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), test.method, endpoint+test.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			for name, values := range test.headers {
				if name == "Host" {
					request.Host = values[0]
				} else {
					request.Header[name] = values
				}
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
			if response.StatusCode != test.want {
				t.Fatalf("status=%d, want %d", response.StatusCode, test.want)
			}
			if test.method == http.MethodHead && len(body) != 0 {
				t.Error("HEAD response contains a body")
			}
			if response.Header.Get("WWW-Authenticate") != "" {
				t.Error("public metadata issued an authentication challenge")
			}
		})
	}
}

func TestMCPRejectsInvalidDeploymentBeforeOpeningProviders(t *testing.T) {
	t.Setenv("MCP_TOKEN", "metadata-test-token")
	for _, test := range []struct {
		name, environment, application, want string
		args                                 []string
	}{
		{name: "unknown runtime environment", environment: "podman", want: "runtime must be"},
		{name: "unknown runtime flag", args: []string{"--runtime=podman"}, want: "runtime must be"},
		{name: "empty runtime flag overrides environment", environment: "docker", args: []string{"--runtime="}, want: "runtime must be"},
		{name: "unknown application", application: "unknown", want: "application must be"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MCP_RUNTIME", test.environment)
			args := append(hostArgs(t.TempDir()), "--bus-url=unsupported://broker")
			args = append(args, test.args...)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			err := cli.RunWithOptions(ctx, args, io.Discard, cli.Options{Application: test.application})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid deployment must be rejected before provider startup: %v", err)
			}
		})
	}
}
