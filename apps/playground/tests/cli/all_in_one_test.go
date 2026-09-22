package cli_test

import (
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
	"sync/atomic"
	"testing"
	"time"

	"commander/apps/playground/cli"
	bridge "commander/apps/playground/internal/mcp"
	"commander/apps/playground/tests/fixture"
)

func TestAllInOneRunsWithoutCheckoutAndCleansUpOnExit(t *testing.T) {
	// Arrange: no checkout files or external Commander services. The reviewed
	// website and scripted LLM use loopback; deployment env is overridden.
	work, temporary := t.TempDir(), t.TempDir()
	t.Chdir(work)
	t.Setenv("TMPDIR", temporary)
	t.Setenv("PATH", filepath.Join(work, "no-installed-tools"))
	for _, name := range []string{"BUS_URL", "STORAGE_URL", "DATABASE_URL", "LOG_LEVEL", "LOG_FORMAT", "HEARTBEAT_TIMEOUT", "COMMANDER_MCP_URL"} {
		t.Setenv(name, "invalid-deployment-value")
	}
	for _, name := range []string{"SERVER_IP_ADDRESS", "SERVER_HOSTNAME", "COMMANDER_DEBUG"} {
		t.Setenv(name, "")
	}
	t.Setenv("COMMANDER_LLM_API_KEY", "local-fixture-key")
	website := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><html lang="en"><title>Release preview</title><h1>Ready for review</h1></html>`)
	}))
	defer website.Close()
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected model path: %s", r.URL.Path)
		}
		var body struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if modelCalls.Add(1) == 1 {
			fixture.SSE(w, map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "id": "missions", "type": "function", "function": map[string]string{"name": "list_missions", "arguments": "{}"}},
				map[string]any{"index": 1, "id": "agents", "type": "function", "function": map[string]string{"name": "list_agents", "arguments": "{}"}},
			}}, "tool_calls")
		} else {
			var results string
			for _, message := range body.Messages {
				if message.Role == "tool" {
					results += message.Content
				}
			}
			if !strings.Contains(results, "review-website") || !strings.Contains(results, "web-quality-agent-") {
				t.Errorf("real mission and agent were not returned to the model: %s", results)
			}
			fixture.SSE(w, map[string]any{"content": "One agent and the review mission are ready."}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	out := new(output)
	done := make(chan error, 1)
	go func() {
		done <- cli.Run(ctx, []string{"--all-in-one", "--provider=other", "--model=fixture", "--base-url=" + model.URL + "/v1", "--no-color"}, input, out, out)
	}()
	finished := false
	defer func() {
		if !finished {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("all-in-one did not shut down after test cancellation")
			}
		}
	}()
	deadline := time.NewTimer(35 * time.Second)
	defer deadline.Stop()
	for !strings.Contains(out.String(), "Commander connected") {
		select {
		case err := <-done:
			finished = true
			t.Fatalf("startup stopped: %v\n%s", err, out.String())
		case <-deadline.C:
			t.Fatalf("all-in-one did not connect:\n%s", out.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	mcpURL := regexp.MustCompile(`http://127\.0\.0\.1:\d+/mcp`).FindString(out.String())
	if mcpURL == "" {
		t.Fatalf("local MCP endpoint was not shown:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "https://defos.ai") {
		t.Fatalf("suggested review target was not shown:\n%s", out.String())
	}
	directories, err := filepath.Glob(filepath.Join(temporary, "commander-playground-*"))
	if err != nil || len(directories) != 1 {
		t.Fatalf("temporary installations=%v, err=%v", directories, err)
	}

	// Act: chat uses the actual MCP, then execute a browser-independent task
	// through its controller and agent against the local test website.
	submit(t, writer, "List missions and available agents")
	until(t, out, "One agent and the review mission are ready.")
	commander, err := bridge.Connect(ctx, mcpURL)
	if err != nil {
		t.Fatal(err)
	}
	defer commander.Close()
	call := func(name string, args any) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(args)
		result, err := commander.Call(ctx, name, raw)
		if err != nil || result.IsError {
			t.Fatalf("%s: %v %s", name, err, result.Text)
		}
		return result.Data
	}
	var inventory struct {
		Agents []struct{ ID, Type, Status string }
	}
	if err := json.Unmarshal(call("list_agents", map[string]any{}), &inventory); err != nil || len(inventory.Agents) != 1 || inventory.Agents[0].Type != "web-quality" || inventory.Agents[0].Status != "ready" {
		t.Fatalf("expected one ready web-quality agent: %+v, err=%v", inventory, err)
	}
	var operation struct{ ID string }
	if err := json.Unmarshal(call("start_operation", map[string]any{"mission_name": "review-website", "input": map[string]any{"url": website.URL, "max_pages": 1}}), &operation); err != nil || operation.ID == "" {
		t.Fatalf("operation not started: %+v, err=%v", operation, err)
	}
	call("assign_task", map[string]any{"operation_id": operation.ID, "action_name": "check-seo", "input": map[string]any{"url": website.URL}})
	for {
		var state struct {
			Tasks []struct {
				Status, Error string
				Result        *struct{ Output struct{ Title string } }
			}
		}
		if err := json.Unmarshal(call("get_operation", map[string]any{"operation_id": operation.ID}), &state); err != nil {
			t.Fatal(err)
		}
		if len(state.Tasks) == 1 && state.Tasks[0].Status == "succeeded" {
			if state.Tasks[0].Result == nil || !strings.Contains(state.Tasks[0].Result.Output.Title, "Release preview") {
				t.Fatalf("test website evidence missing: %+v", state.Tasks[0])
			}
			break
		}
		if len(state.Tasks) == 1 && (state.Tasks[0].Status == "failed" || state.Tasks[0].Status == "interrupted") {
			t.Fatalf("local agent failed: %+v", state.Tasks[0])
		}
		select {
		case <-ctx.Done():
			t.Fatal("local task did not finish")
		case <-time.After(25 * time.Millisecond):
		}
	}
	if err := commander.Close(); err != nil {
		t.Fatal(err)
	}
	submit(t, writer, "/exit")
	select {
	case err := <-done:
		finished = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("all-in-one did not exit")
	}

	// Assert: the terminal exits after every host stops and state is removed.
	if modelCalls.Load() != 2 {
		t.Fatalf("model calls=%d, want 2", modelCalls.Load())
	}
	for _, directory := range directories {
		if _, err := os.Stat(directory); !os.IsNotExist(err) {
			t.Fatalf("temporary state still exists: %s (%v)", directory, err)
		}
	}
	parsed, err := url.Parse(mcpURL)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("local host still owns %s: %v", mcpURL, err)
	}
	_ = listener.Close()
	if entries, err := os.ReadDir(work); err != nil || len(entries) != 0 {
		t.Fatalf("all-in-one wrote working-directory files: %v (%v)", entries, err)
	}
}

func TestAllInOneRejectsExplicitRemoteMCP(t *testing.T) {
	// Arrange: explicit endpoints should not be silently ignored.
	out := new(output)
	// Act.
	err := cli.Run(t.Context(), []string{"--all-in-one", "--mcp-url=http://localhost:4273/mcp"}, strings.NewReader(""), out, out)
	// Assert: fail before any hosts or UI start.
	if err == nil || !strings.Contains(err.Error(), "omit --mcp-url") || strings.Contains(out.String(), "Starting local Commander") {
		t.Fatalf("err=%v output=%s", err, out.String())
	}
}

func TestAllInOneCleansUpWhenSetupIsCanceled(t *testing.T) {
	// Arrange: the user has not connected a model; local hosts still need to
	// stop when the enclosing terminal/session is canceled.
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	t.Setenv("SERVER_IP_ADDRESS", "")
	t.Setenv("SERVER_HOSTNAME", "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	out := new(output)
	done := make(chan error, 1)
	go func() {
		done <- cli.Run(ctx, []string{"--all-in-one", "--provider=", "--debug=false", "--no-color"}, input, out, out)
	}()
	timer := time.NewTimer(35 * time.Second)
	defer timer.Stop()
	for !strings.Contains(out.String(), "Select your model provider") {
		select {
		case err := <-done:
			t.Fatalf("local setup exited early: %v\n%s", err, out.String())
		case <-timer.C:
			t.Fatalf("local setup did not start:\n%s", out.String())
		case <-time.After(20 * time.Millisecond):
		}
	}

	// Act: cancel while the UI is waiting for setup input.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("normal cancellation failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("local setup did not stop")
	}

	// Assert: cancellation performs the same host cleanup as /exit.
	if entries, err := os.ReadDir(temporary); err != nil || len(entries) != 0 {
		t.Fatalf("canceled setup left temporary state: %v (%v)", entries, err)
	}
}
