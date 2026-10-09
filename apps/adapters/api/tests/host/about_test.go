package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api"
	"github.com/defos-ai/commander/apps/adapters/api/cli"
)

func TestAboutIsPublicAndLimitedToDeployment(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	result := server.request(t, http.MethodGet, "/aboutz")
	assertAbout(t, result, "native", "commander-api")
	assertSecurityHeaders(t, result, false)
	if result.header.Get("WWW-Authenticate") != "" {
		t.Error("public metadata unexpectedly requires authentication")
	}
	if !strings.HasPrefix(result.header.Get("Content-Type"), "application/json") {
		t.Errorf("unexpected content type: %q", result.header.Get("Content-Type"))
	}
	head := server.request(t, http.MethodHead, "/aboutz")
	if head.status != http.StatusOK || head.body != "" {
		t.Fatalf("HEAD /aboutz: status=%d body=%q", head.status, head.body)
	}
	assertSecurityHeaders(t, head, false)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		assertAPIError(t, server.request(t, method, "/aboutz"), http.StatusUnauthorized, "authentication_required")
		if result := server.request(t, method, "/aboutz", authenticate); result.status != http.StatusMethodNotAllowed {
			t.Errorf("authenticated %s /aboutz returned %d, want 405", method, result.status)
		}
	}
	for _, path := range []string{"/aboutz/", "/aboutz/private"} {
		assertAPIError(t, server.request(t, http.MethodGet, path), http.StatusUnauthorized, "authentication_required")
	}
}

func TestAboutPreservesHostOriginAndProxyChecks(t *testing.T) {
	for _, test := range []struct {
		name, trusted, host, forwarded, origin string
		status                                 int
		https                                  bool
	}{
		{name: "allowed host", host: "api.example", status: http.StatusOK},
		{name: "unknown host", host: "other.example", status: http.StatusForbidden},
		{name: "cross origin", host: "api.example", origin: "http://other.example", status: http.StatusForbidden},
		{name: "trusted proxy", trusted: "127.0.0.1", host: "internal.example", forwarded: "host=api.example;proto=https", origin: "https://api.example", status: http.StatusOK, https: true},
		{name: "untrusted proxy", trusted: "192.0.2.0/24", host: "internal.example", forwarded: "host=api.example;proto=https", status: http.StatusForbidden},
		{name: "invalid forwarding", trusted: "127.0.0.1", host: "api.example", forwarded: "malformed", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			args := append(fixture.localArgs(), "--allowed-hosts=api.example")
			if test.trusted != "" {
				args = append(args, "--trusted-proxies="+test.trusted, "--proxy-headers=forwarded")
			}
			server := startAPI(t, args)
			result := server.request(t, http.MethodGet, "/aboutz", func(r *http.Request) {
				r.Host = test.host
				if test.forwarded != "" {
					r.Header.Set("Forwarded", test.forwarded)
				}
				if test.origin != "" {
					r.Header.Set("Origin", test.origin)
				}
			})
			if result.status != test.status {
				t.Fatalf("status=%d, want %d: %s", result.status, test.status, result.body)
			}
			assertSecurityHeaders(t, result, test.https)
		})
	}
}

func TestAboutUsesRuntimeConfigurationAndEmbeddedApplication(t *testing.T) {
	for _, test := range []struct {
		name, environment, flag, application, runtime string
	}{
		{name: "Docker environment", environment: "docker", runtime: "docker"},
		{name: "Compose flag", flag: "docker-compose", runtime: "docker-compose"},
		{name: "flag overrides invalid environment", environment: "invalid-runtime", flag: "native", runtime: "native"},
		{name: "embedded native", application: "commander", runtime: "native"},
		{name: "embedded Docker", environment: "docker", application: "commander", runtime: "docker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			t.Setenv("API_RUNTIME", test.environment)
			args := fixture.localArgs()
			if test.flag != "" {
				args = append(args, "--runtime="+test.flag)
			}
			server := startAPI(t, args, cli.Options{Application: test.application})
			application := test.application
			if application == "" {
				application = "commander-api"
			}
			assertAbout(t, server.request(t, http.MethodGet, "/aboutz"), test.runtime, application)
		})
	}
}

func TestInvalidDeploymentFailsBeforeOpeningProviders(t *testing.T) {
	for _, test := range []struct {
		name, environment, flag, application, diagnostic string
	}{
		{name: "runtime environment", environment: "private-invalid-runtime", diagnostic: "runtime"},
		{name: "runtime flag", flag: "private-invalid-runtime", diagnostic: "runtime"},
		{name: "embedded application", application: "private-invalid-application", diagnostic: "application"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			t.Setenv("API_RUNTIME", test.environment)
			args := fixture.localArgs()
			if test.flag != "" {
				args = append(args, "--runtime="+test.flag)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var logs bytes.Buffer
			err := cli.RunWithOptions(ctx, args, &logs, cli.Options{Application: test.application})
			if err == nil || ctx.Err() != nil || !strings.Contains(strings.ToLower(err.Error()), test.diagnostic) {
				t.Fatalf("invalid deployment did not fail promptly with a %s diagnostic: %v", test.diagnostic, err)
			}
			for _, value := range []string{"private-invalid-runtime", "private-invalid-application", "api-fixture-password"} {
				if strings.Contains(err.Error()+logs.String(), value) {
					t.Error("invalid configuration echoed a private configuration value")
				}
			}
			for _, name := range []string{"bus", "database", "storage"} {
				if _, err := os.Stat(filepath.Join(fixture.root, name)); !os.IsNotExist(err) {
					t.Errorf("invalid deployment opened %s before validation", name)
				}
			}
		})
	}
	for _, deployment := range []api.DeploymentConfig{{Runtime: "invalid"}, {Application: "invalid"}} {
		_, err := api.NewHandler(api.Config{
			Security:   api.SecurityConfig{Username: "user", Password: "password", AllowedHosts: []string{"localhost"}},
			Deployment: deployment,
		})
		if err == nil || (!strings.Contains(strings.ToLower(err.Error()), "runtime") && !strings.Contains(strings.ToLower(err.Error()), "application")) {
			t.Fatalf("NewHandler did not validate deployment before dependencies: %v", err)
		}
	}
}

func TestAboutOpenAPIContract(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, append(fixture.localArgs(), "--swagger"))
	document := decodeResponse[struct {
		Paths map[string]struct {
			Get struct {
				Security  json.RawMessage `json:"security"`
				Responses map[string]struct {
					Content map[string]struct {
						Schema struct {
							Ref string `json:"$ref"`
						} `json:"schema"`
					} `json:"content"`
				} `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}](t, server.request(t, http.MethodGet, "/openapi.json", authenticate))
	get := document.Paths["/aboutz"].Get
	if string(get.Security) != "[]" || get.Responses["200"].Content["application/json"].Schema.Ref != "#/components/schemas/About" {
		t.Fatal("OpenAPI must expose the public GET /aboutz response schema")
	}
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(document.Components.Schemas["About"], &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties) != 2 || len(schema.Required) != 2 || !reflect.DeepEqual(schema.Properties["runtime"].Enum, []string{"native", "docker", "docker-compose"}) || !reflect.DeepEqual(schema.Properties["application"].Enum, []string{"commander-api", "commander"}) {
		t.Fatalf("unexpected public deployment schema: %+v", schema)
	}
}

func assertAbout(t *testing.T, result response, runtime, application string) {
	t.Helper()
	body := decodeResponse[map[string]string](t, result)
	if len(body) != 2 || body["runtime"] != runtime || body["application"] != application {
		t.Fatalf("unexpected deployment metadata: %+v", body)
	}
}
