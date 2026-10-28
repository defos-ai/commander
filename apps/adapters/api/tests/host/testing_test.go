package api_test

import (
	"net/http"
	"testing"

	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestStateCleanupIsUnavailableOutsideTestEnvironment(t *testing.T) {
	for _, environment := range []string{"", "production", "development"} {
		t.Run(environment, func(t *testing.T) {
			fixture := hostfixtures.New(t)
			t.Setenv("ENV", environment)
			server := hostfixtures.Start(t, fixture.LocalArgs())
			result := server.Request(t, http.MethodPost, "/api/v1/testing/cleanup", hostfixtures.Authenticate)
			if result.Status != http.StatusNotFound {
				t.Fatalf("cleanup exposed with ENV=%q: status=%d body=%s", environment, result.Status, result.Body)
			}
		})
	}
}
