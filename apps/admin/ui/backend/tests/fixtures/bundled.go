//go:build embed_ui

package fixtures

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/admin/cli"
)

func StartBundledUI(t *testing.T, args ...string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan net.Addr, 1)
	done := make(chan struct{})
	var output bytes.Buffer
	var runErr error
	go func() {
		defer close(done)
		serveArgs := append([]string{"serve"}, args...)
		runErr = cli.RunWithOptions(ctx, append(serveArgs, "--listen-address=127.0.0.1:0"), nil, io.Discard, &output, cli.Options{
			Ready: func(address net.Addr) { ready <- address },
		})
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("bundled UI stopped with error: %v", runErr)
			}
		case <-time.After(6 * time.Second):
			t.Error("bundled UI did not shut down")
		}
	}
	t.Cleanup(stop)
	select {
	case address := <-ready:
		return "http://" + address.String(), stop
	case <-done:
		t.Fatalf("bundled UI startup failed: %v", runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("bundled UI did not become ready")
	}
	return "", stop
}
