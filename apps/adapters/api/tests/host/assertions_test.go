package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func decodeResponse[T any](t *testing.T, result hostfixtures.Response, expectedStatus ...int) T {
	t.Helper()
	status := http.StatusOK
	if len(expectedStatus) > 0 {
		status = expectedStatus[0]
	}
	if result.Status != status {
		t.Fatalf("status=%d, want %d: %s", result.Status, status, result.Body)
	}
	var value T
	if err := json.Unmarshal([]byte(result.Body), &value); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, result.Body)
	}
	return value
}

func assertAPIError(t *testing.T, result hostfixtures.Response, status int, code string) {
	t.Helper()
	var payload map[string]string
	if result.Status != status || json.Unmarshal([]byte(result.Body), &payload) != nil || len(payload) != 1 || payload["error"] != code {
		t.Fatalf("status=%d body=%s, want %d with error %q", result.Status, result.Body, status, code)
	}
}
