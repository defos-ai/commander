package fixtures

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/internal/state"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
)

const HeadlessMission = `{"name":"headless-retained","objectives":["Retain this mission across headless restarts."],"input_schema":{"type":"object"},"output_schema":{"type":"object"}}`

type HeadlessEndpoints struct {
	MCP, API, Admin string
}

type Headless struct {
	Output *fakes.HeadlessOutput
	Errors *fakes.HeadlessOutput
	Input  *fakes.HeadlessInput
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func HeadlessEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"PORT", "BUS_URL", "DATABASE_URL", "STORAGE_URL", "LOG_LEVEL", "LOG_FORMAT", "ENV",
		"API_USERNAME", "API_PASSWORD", "MCP_TOKEN", "SECRETS_ENCRYPTION_KEY",
		"API_ALLOWED_HOSTS", "API_ALLOWED_ORIGINS", "API_TRUSTED_PROXIES", "API_PROXY_HEADERS", "API_SWAGGER_ENABLED", "API_RUNTIME",
		"MCP_RUNTIME", "MCP_LISTEN_ADDRESS", "COMMANDER_AGENT_ID_SUFFIX", "HEARTBEAT_TIMEOUT", "TASK_TIMEOUT",
		"SPAWNER_RUNTIME", "SPAWNER_STATE_DIR", "SPAWNER_DOCKER_NETWORK",
		"COMMANDER_TEMPORARY", "COMMANDER_STATE_DIR", "COMMANDER_DEBUG", "COMMANDER_ADMIN_URL",
		"COMMANDER_API_URL", "COMMANDER_API_USERNAME", "COMMANDER_API_PASSWORD", "COMMANDER_MCP_URL", "COMMANDER_MCP_TOKEN",
		"COMMANDER_LLM_PROVIDER", "COMMANDER_LLM_MODEL", "COMMANDER_LLM_BASE_URL", "COMMANDER_LLM_API_KEY",
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "XAI_API_KEY", "DEEPSEEK_API_KEY",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("API_LISTEN_ADDRESS", "127.0.0.1:0")
	t.Setenv("MCP_LISTEN_ADDRESS", "127.0.0.1:0")
}

func StartHeadless(t *testing.T, directory string, extra ...string) *Headless {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	app := &Headless{Output: &fakes.HeadlessOutput{}, Errors: &fakes.HeadlessOutput{}, Input: &fakes.HeadlessInput{}, cancel: cancel, done: make(chan struct{})}
	args := append([]string{"--all-in-one", "--headless", "--state-dir", directory}, extra...)
	go func() {
		defer close(app.done)
		app.err = cli.Run(ctx, args, app.Input, app.Output, app.Errors)
	}()
	t.Cleanup(func() {
		if err := app.Stop(); err != nil {
			t.Errorf("stop headless Commander: %v", err)
		}
	})
	return app
}

func (h *Headless) AwaitReady(ctx context.Context) (HeadlessEndpoints, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		text := h.Output.String()
		var endpoints HeadlessEndpoints
		for _, line := range strings.Split(text, "\n") {
			switch {
			case strings.HasPrefix(line, "MCP: "):
				endpoints.MCP = strings.TrimPrefix(line, "MCP: ")
			case strings.HasPrefix(line, "API: "):
				endpoints.API = strings.TrimPrefix(line, "API: ")
			case strings.HasPrefix(line, "Admin: "):
				endpoints.Admin = strings.TrimPrefix(line, "Admin: ")
			}
		}
		if strings.HasPrefix(text, "Commander ready\n") && endpoints.MCP != "" && endpoints.API != "" {
			return endpoints, nil
		}
		select {
		case <-h.done:
			return HeadlessEndpoints{}, fmt.Errorf("headless startup ended: %w", errors.Join(h.err, errors.New(h.Errors.String())))
		case <-ctx.Done():
			return HeadlessEndpoints{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *Headless) Stop() error {
	h.cancel()
	select {
	case <-h.done:
		return h.err
	case <-time.After(10 * time.Second):
		return errors.New("headless shutdown did not finish")
	}
}

func HeadlessProfileWithClientData(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	profile, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"settings.json":      []byte("retained invalid client settings"),
		"provider-keys.json": []byte("retained invalid client key cache"),
		"histories":          []byte("retained invalid history directory"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}
