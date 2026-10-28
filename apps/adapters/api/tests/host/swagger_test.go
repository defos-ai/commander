package api_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/cli"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestSwaggerIsDisabledByDefault(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, fixture.LocalArgs())

	for _, path := range []string{"/docs", "/docs/", "/openapi.json"} {
		result := server.Request(t, http.MethodGet, path, hostfixtures.Authenticate)

		if result.Status != http.StatusNotFound {
			t.Errorf("disabled documentation %s returned %d", path, result.Status)
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
			fixture := hostfixtures.New(t)
			t.Setenv("API_SWAGGER_ENABLED", test.environment)
			server := hostfixtures.Start(t, append(fixture.LocalArgs(), test.flag))

			result := server.Request(t, http.MethodGet, "/openapi.json", hostfixtures.Authenticate)

			if result.Status != test.status {
				t.Fatalf("status=%d, want %d", result.Status, test.status)
			}
		})
	}
	t.Run("invalid environment", func(t *testing.T) {
		fixture := hostfixtures.New(t)
		t.Setenv("API_SWAGGER_ENABLED", "invalid-private-value")
		var logs bytes.Buffer

		err := cli.Run(t.Context(), fixture.LocalArgs(), &logs)

		if err == nil || !strings.Contains(err.Error(), "API_SWAGGER_ENABLED") {
			t.Fatalf("invalid boolean did not identify its configuration key: %v", err)
		}
		if strings.Contains(err.Error()+logs.String(), "invalid-private-value") {
			t.Error("invalid environment value leaked to error or logs")
		}
	})
}
