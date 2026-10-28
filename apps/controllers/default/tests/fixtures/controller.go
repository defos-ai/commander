package fixtures

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/controllers/default/cli"
)

func ControllerArgs(root string) []string {
	return []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
		"--database-url=" + filepath.Join(root, "database"),
		"--heartbeat-timeout=45s", "--log-level=error", "--log-format=json",
	}
}

func StartController(t *testing.T, ctx context.Context, args []string) {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	ready, done := make(chan struct{}), make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = cli.RunWithOptions(ctx, args, io.Discard, cli.Options{Ready: func() { close(ready) }})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("controller shutdown: %v", runErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	select {
	case <-ready:
	case <-done:
		t.Fatalf("controller stopped before readiness: %v", runErr)
	case <-ctx.Done():
		t.Fatal("controller did not become ready")
	}
}

func Await(t *testing.T, ctx context.Context, name string, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", name)
		}
	}
}
