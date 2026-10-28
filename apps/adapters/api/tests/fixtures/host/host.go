package host

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/cli"
)

type Fixture struct {
	Root string
	Args []string
}

func New(t *testing.T) Fixture {
	t.Helper()
	for _, name := range []string{"API_LISTEN_ADDRESS", "API_ALLOWED_HOSTS", "API_ALLOWED_ORIGINS", "API_TRUSTED_PROXIES", "API_PROXY_HEADERS", "API_SWAGGER_ENABLED", "API_RUNTIME", "SECRETS_ENCRYPTION_KEY", "PORT", "BUS_URL", "STORAGE_URL", "DATABASE_URL", "LOG_LEVEL", "LOG_FORMAT", "ENV"} {
		t.Setenv(name, "")
	}
	t.Setenv("API_USERNAME", "api-fixture-user")
	t.Setenv("API_PASSWORD", "api-fixture-password")
	root := t.TempDir()
	return Fixture{Root: root, Args: []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
		"--database-url=" + filepath.Join(root, "database"),
		"--log-level=error", "--log-format=json",
	}}
}

func (f Fixture) LocalArgs() []string {
	return append(append([]string(nil), f.Args...), "--listen-address=127.0.0.1:0")
}

type API struct {
	Address net.Addr
	URL     string
	Client  *http.Client
	cancel  context.CancelFunc
	done    <-chan error
	stopped bool
}

func Start(t *testing.T, args []string, supplied ...cli.Options) *API {
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
	server := &API{cancel: cancel, done: done, Client: &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}}
	t.Cleanup(func() {
		server.Stop(t)
		server.Client.CloseIdleConnections()
	})
	select {
	case address := <-ready:
		server.Address = address
		_, port, err := net.SplitHostPort(address.String())
		if err != nil {
			t.Fatal(err)
		}
		server.URL = "http://" + net.JoinHostPort("127.0.0.1", port)
	case err := <-done:
		server.stopped = true
		cancel()
		t.Fatalf("API stopped before becoming ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("API did not become ready within five seconds")
	}
	return server
}

func (s *API) Stop(t *testing.T) {
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

type Response struct {
	Status int
	Header http.Header
	Body   string
}

func (s *API) Request(t *testing.T, method, path string, configure ...func(*http.Request)) Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range configure {
		change(request)
	}
	result, err := s.Client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	return Response{Status: result.StatusCode, Header: result.Header, Body: string(body)}
}
