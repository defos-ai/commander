package fixtures

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

func AffinityGate(t *testing.T) (string, func()) {
	t.Helper()
	path := "/gates/" + id.OperationID()
	fixtureURL := os.Getenv("COMMANDER_TEST_FIXTURE_URL")
	if fixtureURL == "" {
		fixtureURL = "http://127.0.0.1:14276"
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	t.Cleanup(client.CloseIdleConnections)
	release := func() {
		t.Helper()
		// Cleanup must still release the fixture after the test context expires.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, fixtureURL+path, nil)
		if err != nil {
			t.Error(err)
			return
		}
		response, err := client.Do(request)
		if err != nil {
			t.Errorf("release affinity readiness gate: %v", err)
			return
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Errorf("release affinity readiness gate: HTTP %d", response.StatusCode)
		}
	}
	t.Cleanup(release)
	return "http://fixture-releases:8080" + path, release
}
