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
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"commander/apps/playground/cli"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	"commander/framework/id"
	"github.com/charmbracelet/x/ansi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAllInOne(t *testing.T) {
	// Browser presence satisfies mission preflight; this smoke test only executes SEO.
	// Real browser execution is covered by the MCP suite against Compose.
	temporary := t.TempDir()
	browser := filepath.Join(t.TempDir(), "browser-fixture")
	if err := os.WriteFile(browser, []byte("fixture executable, never launched"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHROMIUM_PATH", browser)
	t.Setenv("CHROMIUM_NO_SANDBOX", "false")
	t.Setenv("TMPDIR", temporary)
	t.Setenv("ENV", "")
	t.Setenv("COMMANDER_LLM_API_KEY", "fixture-only")
	t.Setenv("COMMANDER_MCP_URL", "http://127.0.0.1:1/mcp") // The local host wins.
	website := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html lang="en"><title>Release preview</title><meta name="robots" content="noindex"><h1>Preview</h1></html>`)
	}))
	defer website.Close()
	operationID := id.New()
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
		if request == 2 || request == 3 {
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
			delta = toolDelta("start_operation", map[string]any{"operation_id": operationID, "mission_name": "review-website", "input": map[string]any{"url": website.URL}})
		case 4:
			delta = toolDelta("assign_task", map[string]any{"operation_id": operationID, "action_name": "check-seo", "input": map[string]any{"url": website.URL}})
		case 5:
			delta, finish = map[string]any{"content": "**Review running**"}, "stop"
		case 6, 8:
			// Continuations must reach the model automatically, carrying actual
			// results and then the operator's answer, without another chat prompt.
			last := body.Messages[len(body.Messages)-1]
			_, state, ok := strings.Cut(last.Content, "Current state:\n")
			var op operations.Operation
			if !ok || last.Role != "user" || json.Unmarshal([]byte(state), &op) != nil || op.ID != operationID || len(op.Tasks) != 1 || op.Tasks[0].Result == nil || !bytes.Contains(op.Tasks[0].Result.Output, []byte("Release preview")) {
				t.Errorf("model did not receive saved task evidence: %s", last.Content)
				http.Error(w, "missing operation evidence", http.StatusBadRequest)
				return
			}
			if request == 6 {
				task := op.Tasks[0]
				if task.Continuation == nil || task.Continuation.Status != operations.ContinuationPending {
					t.Error("missing model continuation")
					http.Error(w, "missing continuation", http.StatusBadRequest)
					return
				}
				delta = toolDelta("ask_question", map[string]any{"operation_id": operationID, "task_id": task.ID, "version": task.Continuation.Version,
					"text": "Is the indexing restriction intentional?", "options": []string{"No, public release", "Yes, preview only"}})
			} else {
				if len(op.Questions) != 1 || op.Questions[0].Status != operations.QuestionAnswered || op.Questions[0].Answer != "Yes, preview only" {
					t.Errorf("model did not receive operator guidance: %+v", op.Questions)
				}
				delta = toolDelta("complete_operation", map[string]any{"operation_id": operationID, "output": map[string]any{"url": website.URL,
					"summary": "Release preview reviewed; operator confirmed intentional noindex", "findings": []any{}}})
			}
		case 7:
			delta, finish = map[string]any{"content": "Please choose how to interpret the indexing restriction."}, "stop"
		case 9, 10: // Completion can also produce a final background update.
			delta, finish = map[string]any{"content": "**Review complete**\n\n- SEO evidence checked\n- Operator answer recorded"}, "stop"
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
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
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
	client, err := mcp.NewClient(&mcp.Implementation{Name: "playground-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "clean_up_state" {
			t.Fatal("test cleanup tool was exposed in normal mode")
		}
	}

	if _, err := io.WriteString(keyboard, "Review SEO for "+website.URL+"\r"); err != nil {
		t.Fatal(err)
	}
	wait("Yes, preview only")
	if _, err := io.WriteString(keyboard, "\x1b[B\r"); err != nil {
		t.Fatal(err)
	}
	wait("Operator answer recorded")
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "get_report", Arguments: map[string]any{"operation_id": operationID}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	var report reports.Report
	if result.IsError || json.Unmarshal(raw, &report) != nil || report.OperationID != operationID || !bytes.Contains(report.Output, []byte("operator confirmed intentional noindex")) {
		t.Fatalf("Playground did not persist the mission report: %s", raw)
	}
	client.Close()
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

	if requests.Load() < 9 || strings.Contains(output.String(), "**Review complete**") || !strings.Contains(output.String(), "Review complete") {
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

func toolDelta(name string, args map[string]any) map[string]any {
	encoded, _ := json.Marshal(args)
	return map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": name, "type": "function", "function": map[string]any{"name": name, "arguments": string(encoded)}}}}
}

type terminalOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *terminalOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *terminalOutput) String() string {
	b.mu.Lock()
	output := b.buffer.String()
	b.mu.Unlock()
	return ansi.Strip(output)
}
