package default_test

import (
	"context"
	"github.com/defos-ai/commander/apps/controllers/default/cli"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIStartsWithMongo(t *testing.T) {
	databaseURL := os.Getenv("COMMANDER_TEST_MONGO_URL")
	if databaseURL == "" {
		t.Skip("COMMANDER_TEST_MONGO_URL is not set")
	}
	base, query, hasQuery := strings.Cut(databaseURL, "?")
	databaseURL = base + "_controller_startup"
	if hasQuery {
		databaseURL += "?" + query
	}
	t.Setenv("ENV", "")
	root := t.TempDir()
	args := []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
		"--database-url=" + databaseURL,
		"--heartbeat-timeout=45s",
		"--log-level=info", "--log-format=json",
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	ready := false

	err := cli.RunWithOptions(ctx, args, io.Discard, cli.Options{Ready: func() {
		ready = true
		cancel()
	}})

	if err != nil || !ready {
		t.Fatalf("MongoDB host did not start and stop cleanly: ready=%t error=%v", ready, err)
	}
}
