package fixtures

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
)

func HTTPHostArgs(t *testing.T) []string {
	t.Helper()
	for _, name := range []string{"MCP_ALLOWED_HOSTS", "MCP_LISTEN_ADDRESS", "MCP_RUNTIME", "PORT", "ENV"} {
		t.Setenv(name, "")
	}
	t.Setenv("MCP_TOKEN", "mcp-http-host-fixture-token")
	root := t.TempDir()
	return []string{
		"--listen-address=127.0.0.1:0",
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
		"--database-url=" + filepath.Join(root, "database"),
		"--log-level=error", "--log-format=json",
	}
}

type HTTPHost struct {
	URL    string
	client *http.Client
}

func StartHTTPHost(t *testing.T, args []string) *HTTPHost {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	stopped := false
	server := &HTTPHost{client: &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}}
	t.Cleanup(func() {
		cancel()
		defer server.client.CloseIdleConnections()
		if stopped {
			return
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("MCP host did not shut down cleanly: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("MCP host did not shut down within five seconds")
		}
	})
	go func() {
		done <- cli.RunWithOptions(ctx, args, io.Discard, cli.Options{
			Ready: func(address net.Addr) { ready <- address },
		})
	}()
	select {
	case address := <-ready:
		server.URL = "http://" + address.String()
	case err := <-done:
		stopped = true
		t.Fatalf("MCP host stopped before becoming ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("MCP host did not become ready within five seconds")
	}
	return server
}

func (s *HTTPHost) Status(t *testing.T, path, hostname string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = hostname
	response, err := s.client.Do(request)
	if err != nil {
		t.Fatalf("GET %s with Host %s: %v", path, hostname, err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode
}
