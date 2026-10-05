package api_test

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealthAndReadinessWithoutController(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			result := server.request(t, http.MethodGet, path)

			assertHealthy(t, result)
		})
	}
	for _, path := range []string{"/api/v1/missions", "/api/v1/operations", "/docs", "/openapi.json"} {
		t.Run(path, func(t *testing.T) {
			result := server.request(t, http.MethodGet, path)

			if result.status != http.StatusNotFound {
				t.Fatalf("deferred route returned %d, want 404: %s", result.status, result.body)
			}
		})
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run("POST "+path, func(t *testing.T) {
			result := server.request(t, http.MethodPost, path)

			if result.status != http.StatusMethodNotAllowed {
				t.Fatalf("POST returned %d, want 405: %s", result.status, result.body)
			}
		})
	}
}

func TestReadinessReflectsDatabaseFailureAndRecovery(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	missionsPath := filepath.Join(fixture.root, "database", "missions.json")
	if err := os.WriteFile(missionsPath, []byte("invalid fixture-private-data"), 0600); err != nil {
		t.Fatal(err)
	}

	unready := server.request(t, http.MethodGet, "/readyz")
	liveness := server.request(t, http.MethodGet, "/healthz")

	if unready.status != http.StatusServiceUnavailable {
		t.Fatalf("readiness returned %d for unreadable state, want 503: %s", unready.status, unready.body)
	}
	for _, private := range []string{fixture.root, "invalid character", "fixture-private-data", "api-fixture-password"} {
		if strings.Contains(unready.body, private) {
			t.Errorf("readiness exposed private failure detail %q: %s", private, unready.body)
		}
	}
	assertHealthy(t, liveness)

	if err := os.WriteFile(missionsPath, []byte("[]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered := server.request(t, http.MethodGet, "/readyz")

	assertHealthy(t, recovered)
}

func TestCancellationClosesListener(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	assertHealthy(t, server.request(t, http.MethodGet, "/healthz"))

	server.stop(t)
	connection, err := net.DialTimeout("tcp", server.address.String(), time.Second)

	if err == nil {
		connection.Close()
		t.Fatal("API listener still accepts connections after shutdown")
	}
}

func assertHealthy(t *testing.T, result response) {
	t.Helper()
	if result.status != http.StatusOK {
		t.Fatalf("health returned %d, want 200: %s", result.status, result.body)
	}
	if contentType := result.header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("health content type is %q, want application/json", contentType)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(result.body), &body); err != nil {
		t.Fatalf("decode health response: %v; %s", err, result.body)
	}
	if len(body) != 1 || body["status"] != "ok" {
		t.Fatalf("unexpected health response: %s", result.body)
	}
}
