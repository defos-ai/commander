package mcp_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	"github.com/defos-ai/commander/framework/host"
)

func hostArgs(root string) []string {
	return []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
		"--database-url=" + filepath.Join(root, "database"),
		"--listen-address=127.0.0.1:0", "--log-level=debug", "--log-format=json",
	}
}

func startMCP(t *testing.T, credentials *host.BearerCredentials, logs io.Writer) (string, *http.Client, func()) {
	t.Helper()
	return startMCPWithOptions(t, nil, cli.Options{Credentials: credentials}, logs)
}

func startMCPWithOptions(t *testing.T, extraArgs []string, options cli.Options, logs io.Writer) (string, *http.Client, func()) {
	t.Helper()
	t.Setenv("ENV", "")
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	args := append(hostArgs(t.TempDir()), extraArgs...)
	options.Ready = func(address net.Addr) { ready <- address }
	go func() {
		done <- cli.RunWithOptions(ctx, args, logs, options)
	}()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("MCP shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("MCP did not stop within five seconds")
		}
	}
	t.Cleanup(stop)
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	t.Cleanup(transport.CloseIdleConnections)
	select {
	case address := <-ready:
		return "http://" + address.String(), client, stop
	case err := <-done:
		stopped = true
		cancel()
		t.Fatalf("MCP stopped before becoming ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("MCP did not become ready within five seconds")
	}
	return "", nil, nil
}
