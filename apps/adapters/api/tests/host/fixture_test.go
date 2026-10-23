package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/cli"
)

type fixture struct {
	root string
	args []string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	for _, name := range []string{"API_LISTEN_ADDRESS", "API_ALLOWED_HOSTS", "API_ALLOWED_ORIGINS", "API_TRUSTED_PROXIES", "API_PROXY_HEADERS", "API_SWAGGER_ENABLED", "API_RUNTIME", "SECRETS_ENCRYPTION_KEY", "PORT", "BUS_URL", "STORAGE_URL", "DATABASE_URL", "LOG_LEVEL", "LOG_FORMAT", "ENV"} {
		t.Setenv(name, "")
	}
	t.Setenv("API_USERNAME", "api-fixture-user")
	t.Setenv("API_PASSWORD", "api-fixture-password")
	root := t.TempDir()
	return fixture{root: root, args: []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
		"--database-url=" + filepath.Join(root, "database"),
		"--log-level=error", "--log-format=json",
	}}
}

func (f fixture) localArgs() []string {
	return append(append([]string(nil), f.args...), "--listen-address=127.0.0.1:0")
}

type runningAPI struct {
	address net.Addr
	url     string
	client  *http.Client
	cancel  context.CancelFunc
	done    <-chan error
	stopped bool
}

func startAPI(t *testing.T, args []string, supplied ...cli.Options) *runningAPI {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	var options cli.Options
	if len(supplied) != 0 {
		options = supplied[0]
	}
	options.Ready = func(address net.Addr) { ready <- address }
	go func() {
		done <- cli.RunWithOptions(ctx, args, io.Discard, options)
	}()
	server := &runningAPI{cancel: cancel, done: done, client: &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}}
	t.Cleanup(func() {
		server.stop(t)
		server.client.CloseIdleConnections()
	})
	select {
	case address := <-ready:
		server.address = address
		_, port, err := net.SplitHostPort(address.String())
		if err != nil {
			t.Fatal(err)
		}
		server.url = "http://" + net.JoinHostPort("127.0.0.1", port)
	case err := <-done:
		server.stopped = true
		cancel()
		t.Fatalf("API stopped before becoming ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("API did not become ready within five seconds")
	}
	return server
}

func (s *runningAPI) stop(t *testing.T) {
	t.Helper()
	if s.stopped {
		return
	}
	s.cancel()
	select {
	case err := <-s.done:
		s.stopped = true
		if err != nil {
			t.Errorf("API did not shut down cleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("API did not shut down within five seconds")
	}
}

type response struct {
	status int
	header http.Header
	body   string
}

func (s *runningAPI) request(t *testing.T, method, path string, configure ...func(*http.Request)) response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, s.url+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range configure {
		change(request)
	}
	result, err := s.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: result.StatusCode, header: result.Header, body: string(body)}
}

func decodeResponse[T any](t *testing.T, result response, expectedStatus ...int) T {
	t.Helper()
	status := http.StatusOK
	if len(expectedStatus) > 0 {
		status = expectedStatus[0]
	}
	if result.status != status {
		t.Fatalf("status=%d, want %d: %s", result.status, status, result.body)
	}
	var value T
	if err := json.Unmarshal([]byte(result.body), &value); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, result.body)
	}
	return value
}

func assertAPIError(t *testing.T, result response, status int, code string) {
	t.Helper()
	var payload map[string]string
	if result.status != status || json.Unmarshal([]byte(result.body), &payload) != nil || len(payload) != 1 || payload["error"] != code {
		t.Fatalf("status=%d body=%s, want %d with error %q", result.status, result.body, status, code)
	}
}
