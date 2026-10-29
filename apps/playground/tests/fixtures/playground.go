package fixtures

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
)

func PlaygroundEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"COMMANDER_STATE_DIR", "COMMANDER_TEMPORARY", "COMMANDER_LLM_PROVIDER", "COMMANDER_LLM_MODEL", "COMMANDER_LLM_BASE_URL", "COMMANDER_DEBUG", "COMMANDER_MCP_URL", "COMMANDER_API_URL", "COMMANDER_API_USERNAME", "COMMANDER_API_PASSWORD", "COMMANDER_MCP_TOKEN", "COMMANDER_ADMIN_URL"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, preset := range llm.Presets() {
		for _, name := range preset.KeyEnv {
			t.Setenv(name, "")
		}
	}
}

type Playground struct {
	t      *testing.T
	ctx    context.Context
	done   <-chan error
	output fakes.TerminalOutput
}

func StartPlayground(t *testing.T, args []string) *Playground {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	input, keyboard := io.Pipe()
	done := make(chan error, 1)
	stopped := make(chan struct{})
	p := &Playground{t: t, ctx: ctx, done: done}
	t.Cleanup(func() {
		cancel()
		keyboard.Close()
		input.Close()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("Playground did not stop")
		}
	})
	go func() {
		defer close(stopped)
		done <- cli.Run(ctx, append(args, "--no-color"), input, &p.output, &p.output)
	}()
	return p
}

func (p *Playground) Wait(text string) string {
	p.t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		output := p.output.String()
		if strings.Contains(output, text) {
			return output
		}
		select {
		case err := <-p.done:
			p.t.Fatalf("Playground stopped before %q: %v", text, err)
		case <-p.ctx.Done():
			p.t.Fatalf("waiting for %q: %s", text, output)
		case <-tick.C:
		}
	}
}
