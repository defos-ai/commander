package api_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"commander/adapters/api/cli"
)

type fixture struct {
	root string
	args []string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	for _, name := range []string{"API_LISTEN_ADDRESS", "PORT", "BUS_URL", "STORAGE_URL", "DATABASE_URL", "LOG_LEVEL", "LOG_FORMAT", "ENV"} {
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

func startAPI(t *testing.T, args []string) *runningAPI {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- cli.RunWithOptions(ctx, args, io.Discard, cli.Options{Ready: func(address net.Addr) {
			ready <- address
		}})
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

func (s *runningAPI) request(t *testing.T, method, path string) response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, s.url+path, nil)
	if err != nil {
		t.Fatal(err)
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
