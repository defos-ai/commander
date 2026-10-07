package api_test

import (
	"bytes"
	"github.com/defos-ai/commander/apps/adapters/api/cli"
	"net/http"
	"strings"
	"testing"
)

func TestSwaggerIsDisabledByDefault(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())

	for _, path := range []string{"/docs", "/docs/", "/openapi.json"} {
		result := server.request(t, http.MethodGet, path, authenticate)

		if result.status != http.StatusNotFound {
			t.Errorf("disabled documentation %s returned %d", path, result.status)
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
