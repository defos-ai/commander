package api_test

import (
	"net/http"
	"testing"
)

func TestStateCleanupIsUnavailableOutsideTestEnvironment(t *testing.T) {
	for _, environment := range []string{"", "production", "development"} {
		t.Run(environment, func(t *testing.T) {
			fixture := newFixture(t)
			t.Setenv("ENV", environment)
			server := startAPI(t, fixture.localArgs())
			result := server.request(t, http.MethodPost, "/api/v1/testing/cleanup", authenticate)
			if result.status != http.StatusNotFound {
				t.Fatalf("cleanup exposed with ENV=%q: status=%d body=%s", environment, result.status, result.body)
			}
		})
	}
}
