package fixtures

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/internal/state"
)

const backgroundChildEnvironment = "COMMANDER_TEST_BACKGROUND_CHILD"

func RunBackgroundChild() (int, bool) {
	if os.Getenv(backgroundChildEnvironment) != "1" || len(os.Args) < 5 || os.Args[1] != "--all-in-one" || os.Args[2] != "--headless" || os.Args[3] != "--state-dir" {
		return 0, false
	}
	return cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr), true
}

func BackgroundProfile(t *testing.T) string {
	t.Helper()
	t.Setenv(backgroundChildEnvironment, "1")
	directory := filepath.Join(t.TempDir(), "profile")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cli.Run(ctx, []string{"stop", "--state-dir", directory}, nil, io.Discard, io.Discard); err != nil {
			t.Errorf("stop background Commander: %v", err)
		}
	})
	return directory
}

func OccupyAPIAddress(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv("API_LISTEN_ADDRESS", listener.Addr().String())
}

func HoldBackendLease(t *testing.T, directory string) *os.File {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := state.LockFile(root, ".lock")
	if err != nil {
		_ = root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = lease.Close()
		_ = root.Close()
	})
	return lease
}

func AwaitShutdownRequest(ctx context.Context, directory, instanceID string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(directory, "stop-"+instanceID)); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
