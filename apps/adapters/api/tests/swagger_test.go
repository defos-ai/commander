package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"commander/adapters/api/cli"
)

var documentationPaths = []string{"/docs", "/docs/", "/docs/swagger-ui.css", "/docs/swagger-ui-bundle.js", "/docs/initializer.js", "/openapi.json"}

func TestSwaggerIsDisabledByDefault(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())

	for _, path := range documentationPaths {
		result := server.request(t, http.MethodGet, path, authenticate)

		if result.status != http.StatusNotFound {
			t.Errorf("disabled documentation %s returned %d", path, result.status)
		}
	}
}

func TestSwaggerServesProtectedLocalUIAndOpenAPI(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_SWAGGER_ENABLED", "true")
	server := startAPI(t, fixture.localArgs())
	server.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	redirect := server.request(t, http.MethodGet, "/docs", authenticate)
	html := server.request(t, http.MethodGet, "/docs/", authenticate)
	spec := server.request(t, http.MethodGet, "/openapi.json", authenticate)

	if redirect.status < 300 || redirect.status >= 400 || redirect.header.Get("Location") != "/docs/" {
		t.Fatalf("unexpected docs redirect: status=%d location=%q", redirect.status, redirect.header.Get("Location"))
	}
	if html.status != http.StatusOK || !strings.Contains(html.header.Get("Content-Type"), "text/html") {
		t.Fatalf("docs did not serve HTML: status=%d content-type=%q", html.status, html.header.Get("Content-Type"))
	}
	assertSecurityHeaders(t, html, false)
	assertSecurityHeaders(t, spec, false)
	if policy := html.header.Get("Content-Security-Policy"); !strings.Contains(policy, "script-src 'self'") || strings.Contains(policy, "unsafe-eval") {
		t.Errorf("Swagger scripts should load locally without unsafe eval: %q", policy)
	}
	if strings.Contains(spec.header.Get("Content-Security-Policy"), "unsafe-inline") {
		t.Error("Swagger HTML policy leaked to API JSON responses")
	}
	for _, path := range documentationPaths {
		assertAPIError(t, server.request(t, http.MethodGet, path), http.StatusUnauthorized, "authentication_required")
	}
	for _, path := range []string{"/docs/swagger-ui.css", "/docs/swagger-ui-bundle.js", "/docs/initializer.js"} {
		if !strings.Contains(html.body, path) {
			t.Errorf("docs HTML does not reference local asset %s", path)
		}
		asset := server.request(t, http.MethodGet, path, authenticate)
		if asset.status != http.StatusOK || len(asset.body) == 0 {
			t.Fatalf("local asset %s failed: status=%d", path, asset.status)
		}
		if path == "/docs/swagger-ui-bundle.js" && !strings.Contains(asset.body, "SwaggerUIBundle") {
			t.Error("docs bundle is not Swagger UI")
		}
	}
	type documentSpec struct {
		OpenAPI  string                `json:"openapi"`
		Security []map[string][]string `json:"security"`
		Paths    map[string]struct {
			Get struct {
				Security  json.RawMessage            `json:"security"`
				Responses map[string]json.RawMessage `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
		Components struct {
			SecuritySchemes map[string]struct {
				Type   string `json:"type"`
				Scheme string `json:"scheme"`
			} `json:"securitySchemes"`
		} `json:"components"`
	}
	document := decodeResponse[documentSpec](t, spec)
	if !strings.HasPrefix(document.OpenAPI, "3.") || len(document.Security) != 1 {
		t.Fatalf("missing OpenAPI version or global security: %+v", document)
	}
	if _, ok := document.Security[0]["basicAuth"]; !ok {
		t.Errorf("global security does not require Basic authentication: %+v", document.Security)
	}
	if scheme := document.Components.SecuritySchemes["basicAuth"]; scheme.Type != "http" || scheme.Scheme != "basic" {
		t.Errorf("incorrect Basic authentication schema: %+v", scheme)
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		if string(document.Paths[path].Get.Security) != "[]" {
			t.Errorf("public probe %s does not override global security", path)
		}
	}
	for _, path := range []string{"/api/v1/missions", "/api/v1/missions/{mission_name}", "/api/v1/actions", "/api/v1/actions/{action_name}", "/api/v1/agents", "/api/v1/agents/{agent_id}"} {
		endpoint, ok := document.Paths[path]
		if !ok || endpoint.Get.Responses["200"] == nil {
			t.Errorf("OpenAPI does not document GET %s", path)
		}
		if string(endpoint.Get.Security) == "[]" {
			t.Errorf("protected catalog %s declared public", path)
		}
	}
}

func TestSwaggerConfigurationPrecedence(t *testing.T) {
	for _, test := range []struct {
		name, environment, flag string
		status                  int
	}{
		{name: "flag disables environment", environment: "true", flag: "--swagger=false", status: http.StatusNotFound},
		{name: "flag overrides invalid environment", environment: "invalid-private-value", flag: "--swagger=true", status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			t.Setenv("API_SWAGGER_ENABLED", test.environment)
			server := startAPI(t, append(fixture.localArgs(), test.flag))

			result := server.request(t, http.MethodGet, "/openapi.json", authenticate)

			if result.status != test.status {
				t.Fatalf("status=%d, want %d", result.status, test.status)
			}
		})
	}
	t.Run("invalid environment", func(t *testing.T) {
		fixture := newFixture(t)
		t.Setenv("API_SWAGGER_ENABLED", "invalid-private-value")
		var logs bytes.Buffer

		err := cli.Run(t.Context(), fixture.localArgs(), &logs)

		if err == nil || !strings.Contains(err.Error(), "API_SWAGGER_ENABLED") {
			t.Fatalf("invalid boolean did not identify its configuration key: %v", err)
		}
		if strings.Contains(err.Error()+logs.String(), "invalid-private-value") {
			t.Error("invalid environment value leaked to error or logs")
		}
	})
}
