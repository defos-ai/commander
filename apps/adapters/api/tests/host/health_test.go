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

	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestReadinessReflectsDatabaseFailureAndRecovery(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	missionsPath := filepath.Join(fixture.Root, "database", "missions.json")
	if err := os.WriteFile(missionsPath, []byte("invalid fixture-private-data"), 0600); err != nil {
		t.Fatal(err)
	}

	unready := server.Request(t, http.MethodGet, "/readyz")
	liveness := server.Request(t, http.MethodGet, "/healthz")

	if unready.Status != http.StatusServiceUnavailable {
		t.Fatalf("readiness returned %d for unreadable state, want 503: %s", unready.Status, unready.Body)
	}
	for _, private := range []string{fixture.Root, "invalid character", "fixture-private-data", "api-fixture-password"} {
		if strings.Contains(unready.Body, private) {
			t.Errorf("readiness exposed private failure detail %q: %s", private, unready.Body)
		}
	}
	assertHealthy(t, liveness)

	if err := os.WriteFile(missionsPath, []byte("[]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered := server.Request(t, http.MethodGet, "/readyz")

	assertHealthy(t, recovered)
}

func TestCancellationClosesListener(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	assertHealthy(t, server.Request(t, http.MethodGet, "/healthz"))

	server.Stop(t)
	connection, err := net.DialTimeout("tcp", server.Address.String(), time.Second)

	if err == nil {
		connection.Close()
		t.Fatal("API listener still accepts connections after shutdown")
	}
}

func assertHealthy(t *testing.T, result hostfixtures.Response) {
	t.Helper()
	if result.Status != http.StatusOK {
		t.Fatalf("health returned %d, want 200: %s", result.Status, result.Body)
	}
	if contentType := result.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("health content type is %q, want application/json", contentType)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(result.Body), &body); err != nil {
		t.Fatalf("decode health response: %v; %s", err, result.Body)
	}
	if len(body) != 1 || body["status"] != "ok" {
		t.Fatalf("unexpected health response: %s", result.Body)
	}
}
