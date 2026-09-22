package playground_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"commander/apps/playground/cli"
	"github.com/charmbracelet/x/ansi"
)

func TestAllInOne(t *testing.T) {
	// Arrange: real bundled hosts and a deterministic local model. No browser,
	// website review, external service or real API key is needed for this test.
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	t.Setenv("COMMANDER_LLM_API_KEY", "fixture-only")
	t.Setenv("COMMANDER_MCP_URL", "http://127.0.0.1:1/mcp") // The local host wins.
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		request := requests.Add(1)
		if request > 1 {
			want := "review-website"
			if request == 3 {
				want = "web-quality-agent-"
			}
			if len(body.Messages) == 0 || body.Messages[len(body.Messages)-1].Role != "tool" || !strings.Contains(body.Messages[len(body.Messages)-1].Content, want) {
				t.Errorf("model did not receive the real tool result containing %q", want)
			}
		}
		var delta map[string]any
		finish := "tool_calls"
		switch request {
		case 1, 2:
			name := "list_missions"
			if request == 2 {
				name = "list_agents"
			}
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": name, "type": "function", "function": map[string]any{"name": name, "arguments": "{}"}}}}
		case 3:
			delta, finish = map[string]any{"content": "**Ready**\n\n- Mission loaded\n- Agent connected"}, "stop"
		default:
			t.Errorf("unexpected model request %d", request)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
	defer model.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	input, keyboard := io.Pipe()
	defer input.Close()
	defer keyboard.Close()
	var output terminalOutput
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- cli.Run(ctx, []string{"--all-in-one", "--provider=other", "--model=fixture", "--base-url=" + model.URL + "/v1", "--no-color", "--debug=false"}, input, &output, &output)
	}()
	wait := func(text string) {
		t.Helper()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for !strings.Contains(output.String(), text) {
			select {
			case err := <-done:
				t.Fatalf("CLI stopped before %q: %v\n%s", text, err, output.String())
			case <-tick.C:
			case <-ctx.Done():
				t.Fatalf("waiting for %q: %s", text, output.String())
			}
		}
	}
	// Unblock input and stop the hosts even if a rendering assertion fails.
	t.Cleanup(func() {
		cancel()
		_ = keyboard.Close()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("Playground did not clean up its local hosts")
		}
	})
	wait("Commander connected")
	endpoint := regexp.MustCompile(`http://127\.0\.0\.1:\d+/mcp`).FindString(output.String())
	if endpoint == "" {
		t.Fatalf("missing local endpoint: %s", output.String())
	}

	// Act: one short chat exercises model tools through the real MCP hosts.
	if _, err := io.WriteString(keyboard, "List missions and agents\r"); err != nil {
		t.Fatal(err)
	}
	wait("Agent connected")
	if _, err := io.WriteString(keyboard, "/exit\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("/exit did not stop the CLI")
	}

	// Assert: Markdown was rendered, and exiting removed temporary data and the
	// MCP listener. Nothing from the test remains running in the background.
	if requests.Load() != 3 || strings.Contains(output.String(), "**Ready**") || !strings.Contains(output.String(), "Ready") {
		t.Fatalf("unexpected chat rendering: %s", output.String())
	}
	files, err := os.ReadDir(temporary)
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary state remains after exit: %v (%v)", files, err)
	}
	address, _ := url.Parse(endpoint)
	connection, err := net.DialTimeout("tcp", address.Host, time.Second)
	if err == nil {
		_ = connection.Close()
		t.Fatal("local MCP is still listening after /exit")
	}
}

type terminalOutput struct {
	sync.Mutex
	buffer bytes.Buffer
}

func (b *terminalOutput) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.buffer.Write(p)
}

func (b *terminalOutput) String() string {
	b.Lock()
	defer b.Unlock()
	return ansi.Strip(b.buffer.String())
}
