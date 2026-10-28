package fixtures

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	examplecli "github.com/defos-ai/commander-example-agent/cli"
	"github.com/defos-ai/commander/apps/playground/internal/local"
)

func StartLocalAdminSession(t *testing.T, options local.Options) (*local.Session, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	session, err := local.StartWithOptions(ctx, options)
	if err != nil {
		t.Fatalf("start all-in-one: %v", err)
	}
	stop := func() {
		if err := session.Close(); err != nil {
			t.Errorf("stop all-in-one: %v", err)
		}
	}
	t.Cleanup(stop)
	return session, stop
}

func StartLocalExampleHost(t *testing.T, directory string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	args := []string{"--bus-url=" + filepath.Join(directory, "bus"), "--storage-url=" + filepath.Join(directory, "storage")}
	done := make(chan error, 1)
	go func() { done <- examplecli.Run(ctx, args, io.Discard) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("stop workflow agent: %v", err)
			}
		case <-timer.C:
			t.Error("workflow agent did not stop before session cleanup")
		}
	}
	t.Cleanup(stop)
	return stop
}

func AllInOneEnvironment(t *testing.T) string {
	t.Helper()
	temporary := t.TempDir()
	frontend := t.TempDir()
	externalSpawner := t.TempDir()
	externalBuilder := t.TempDir()
	t.Setenv("SPAWNER_RUNTIME", "docker")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "")
	t.Setenv("SPAWNER_STATE_DIR", externalSpawner)
	t.Setenv("BUILDER_WORKSPACE_DIR", externalBuilder)
	t.Cleanup(func() {
		if files, err := os.ReadDir(externalSpawner); err != nil || len(files) != 0 {
			t.Errorf("all-in-one used the external spawner state directory: %v (%v)", files, err)
		}
		if files, err := os.ReadDir(externalBuilder); err != nil || len(files) != 0 {
			t.Errorf("all-in-one used the external builder workspace directory: %v (%v)", files, err)
		}
	})
	if err := os.WriteFile(filepath.Join(frontend, "index.html"), []byte(`<html>Admin fixture</html>`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temporary)
	}
	t.Setenv("ENV", "")
	t.Setenv("TASK_TIMEOUT", "")
	t.Setenv("COMMANDER_LLM_API_KEY", "fixture-only")
	t.Setenv("COMMANDER_MCP_URL", "http://127.0.0.1:1/mcp") // The local host wins.
	t.Setenv("COMMANDER_MCP_TOKEN", "invalid remote token") // Local credentials win too.
	t.Setenv("COMMANDER_ADMIN_URL", "")
	t.Setenv("COMMANDER_API_URL", "invalid remote API URL") // Embedded credentials and URL win.
	t.Setenv("COMMANDER_API_USERNAME", "remote-operator")
	t.Setenv("COMMANDER_API_PASSWORD", "remote-password")
	t.Setenv("MCP_TOKEN", "all-in-one-mcp-secret")
	t.Setenv("MCP_RUNTIME", "native")
	t.Setenv("API_USERNAME", "commander")
	t.Setenv("API_PASSWORD", "all-in-one-api-secret")
	t.Setenv("API_LISTEN_ADDRESS", "127.0.0.1:0")
	t.Setenv("API_RUNTIME", "native")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "")
	t.Setenv("UI_LISTEN_ADDRESS", "127.0.0.1:0")
	t.Setenv("UI_STATIC_DIR", frontend)
	t.Setenv("UI_PUBLIC_ORIGIN", "")
	t.Setenv("UI_API_UPSTREAMS", "")
	t.Setenv("PORT", "not-a-port") // All-in-one uses explicit API and Admin listeners.
	t.Setenv("API_ALLOWED_HOSTS", "localhost,127.0.0.1,::1")
	t.Setenv("API_ALLOWED_ORIGINS", "")
	t.Setenv("API_TRUSTED_PROXIES", "")
	t.Setenv("API_PROXY_HEADERS", "none")
	t.Setenv("API_SWAGGER_ENABLED", "false")
	return temporary
}
