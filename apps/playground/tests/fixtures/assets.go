package fixtures

import (
	"io"
	"net/http"
	"testing"
	"time"
)

func FetchLocalAsset(t *testing.T, baseURL, path string) ([]byte, http.Header) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Commander-UI", "1")
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("fetch %s: status=%d error=%v", path, response.StatusCode, err)
	}
	return body, response.Header
}
