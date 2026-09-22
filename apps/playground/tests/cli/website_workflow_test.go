package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"commander/apps/playground/cli"
	bridge "commander/apps/playground/internal/mcp"
	"commander/apps/playground/tests/fixture"
)

func TestPlaygroundWebsiteReviewThroughRealCommander(t *testing.T) {
	if testing.Short() {
		t.Skip("builds three executables and runs a real browser")
	}
	browser := os.Getenv("CHROMIUM_PATH")
	if browser == "" {
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
			if path, err := exec.LookPath(name); err == nil {
				browser = path
				break
			}
		}
	}
	if browser == "" {
		t.Skip("set CHROMIUM_PATH to run the real website workflow")
	}

	// Arrange: only the model is scripted; CLI, MCP, controller, agent and browser are real.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../../../.."))
	state := t.TempDir()
	env := append(os.Environ(), "BUS_URL="+filepath.Join(state, "bus"), "STORAGE_URL="+filepath.Join(state, "storage"), "DATABASE_URL="+filepath.Join(state, "database"), "LOG_LEVEL=info", "LOG_FORMAT=json", "CHROMIUM_PATH="+browser, "CHROMIUM_NO_SANDBOX=true")
	start := func(name, pkg string, args ...string) *output {
		t.Helper()
		binary := filepath.Join(state, name)
		build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-race", "-o", binary, pkg)
		build.Dir = root
		if data, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, data)
		}
		logs := new(output)
		command := exec.CommandContext(ctx, binary, args...)
		command.Env, command.Stdout, command.Stderr = env, logs, logs
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		t.Cleanup(func() {
			_ = command.Process.Signal(os.Interrupt)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = command.Process.Kill()
				<-done
				t.Errorf("%s did not stop", name)
			}
			if t.Failed() {
				t.Logf("%s logs:\n%s", name, logs.String())
			}
		})
		return logs
	}
	controller := start("controller", "./apps/controllers/default/cmd", "--with-missions="+filepath.Join(root, "apps/controllers/default/data/default-missions.json"))
	until(t, controller, "Controller ready")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	mcpLogs := start("mcp", "./apps/adapters/mcp/cmd/commander-mcp", "--listen-address="+address)
	until(t, mcpLogs, "MCP server listening")
	agent := start("agent", "./apps/agents/web-quality/cmd", "--heartbeat-interval=100ms")
	until(t, agent, "agent ready")
	commander, err := bridge.Connect(ctx, "http://"+address+"/mcp")
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
	deadline := time.Now().Add(10 * time.Second)
	for {
		var listed struct{ Agents []json.RawMessage }
		_ = json.Unmarshal(call("list_agents", map[string]any{}), &listed)
		if len(listed.Agents) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent was not discovered")
		}
		time.Sleep(50 * time.Millisecond)
	}
	website := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><html lang="en"><title>Release preview</title><meta name="generator" content="WordPress 6.6.2"><h1>Welcome</h1><a href="/missing">Missing page</a></html>`)
	}))
	defer website.Close()
	const operationID = "181cb18f-dc0a-4d7e-94f6-a9d96b02f52e"
	const summary = "Fix the broken link before release."
	missionInput := map[string]any{"url": website.URL, "max_pages": 1}
	questionIDs := make(chan string, 1)
	var asked, backgroundReviews atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role, Content string
				ToolCallID    string `json:"tool_call_id"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
			t.Errorf("invalid model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		last := body.Messages[len(body.Messages)-1]
		tool, args, text := "", map[string]any{}, ""
		operationArgs := func() map[string]any { return map[string]any{"operation_id": operationID} }
		switch {
		case last.Role == "tool":
			if strings.HasPrefix(last.Content, "Tool failed:") {
				t.Errorf("MCP failed: %s", last.Content)
			}
			switch strings.TrimPrefix(last.ToolCallID, "call-") {
			case "start_operation":
				tool, args = "assign_task", operationArgs()
				args["action_name"], args["input"] = "discover-pages", missionInput
			case "assign_task":
				text = "Website checks are running."
			case "ask_question":
				var question struct{ ID string }
				_ = json.Unmarshal([]byte(last.Content), &question)
				questionIDs <- question.ID
				text = "Waiting for release guidance."
			case "get_operation":
				var op bridge.Operation
				_ = json.Unmarshal([]byte(last.Content), &op)
				tool, args = "resolve_continuation", operationArgs()
				for _, task := range op.Tasks {
					if task.ActionName == "discover-pages" && task.Continuation != nil {
						args["task_id"], args["version"] = task.ID, task.Continuation.Version
					}
				}
				args["actions"] = []any{}
			case "resolve_continuation":
				tool, args = "complete_operation", operationArgs()
				args["output"] = map[string]any{"url": website.URL, "summary": summary, "findings": []any{map[string]any{"page_url": website.URL, "check": "check-links", "description": "The missing page returns HTTP 404."}}}
			case "complete_operation":
				text = "Review complete: " + summary
			}
		case strings.HasPrefix(last.Content, "Background operation update"):
			_, raw, _ := strings.Cut(last.Content, "Current state:\n")
			var op bridge.Operation
			if err := json.Unmarshal([]byte(raw), &op); err != nil {
				t.Errorf("invalid background state: %v", err)
			}
			if op.Terminal() {
				text = "Saved website review is complete."
			} else if asked.Load() != 0 {
				tool, args = "get_operation", operationArgs()
			} else {
				backgroundReviews.Add(1)
				if len(op.Tasks) != 6 {
					t.Errorf("grouped review started with %d tasks", len(op.Tasks))
				}
				tool, args = "ask_question", operationArgs()
				for _, task := range op.Tasks {
					if task.Status != "succeeded" {
						t.Errorf("review preceded completed check: %+v", task)
					}
					if task.ActionName == "discover-pages" && task.Continuation != nil {
						args["task_id"], args["version"] = task.ID, task.Continuation.Version
					}
				}
				args["text"], args["options"] = "Should the missing page block release?", []string{"Yes", "No"}
				asked.Add(1)
			}
		case strings.HasPrefix(last.Content, "The operator answered"):
			tool, args = "get_operation", operationArgs()
		default:
			tool, args = "start_operation", operationArgs()
			args["mission_name"], args["input"] = "review-website", missionInput
		}
		if tool != "" {
			raw, _ := json.Marshal(args)
			fixture.SSE(w, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call-" + tool, "type": "function", "function": map[string]string{"name": tool, "arguments": string(raw)}}}}, "tool_calls")
		} else {
			fixture.SSE(w, map[string]any{"content": text}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	t.Setenv("COMMANDER_LLM_API_KEY", "fixture-secret")
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	out := new(output)
	done := make(chan error, 1)
	go func() {
		done <- cli.Run(ctx, []string{"--provider=other", "--model=fixture", "--base-url=" + model.URL, "--mcp-url=http://" + address + "/mcp", "--no-color"}, input, out, out)
	}()
	until(t, out, "Commander connected")

	// Act: one objective triggers automation and background review; only answer the displayed question.
	submit(t, writer, "Review the website before release.")
	var questionID string
	select {
	case questionID = <-questionIDs:
	case <-time.After(50 * time.Second):
		terminal := out.String()
		t.Fatalf("automatic review did not ask its question:\n%s", terminal[:min(len(terminal), 8000)])
	}
	if questionID == "" {
		t.Fatal("question was not saved")
	}
	until(t, out, "Should the missing page block release?")
	until(t, out, "Enter answer")
	// Choose Yes with the selector; the operator never needs a question ID.
	if _, err := io.WriteString(writer, "\r"); err != nil {
		t.Fatal(err)
	}
	until(t, out, "Review complete:")
	submit(t, writer, "/exit")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Playground did not exit")
	}

	// Assert: automatic checks, the operator's answer and the final report persisted through MCP.
	op, err := commander.Operation(ctx, operationID)
	if err != nil || op.Status != "completed" || len(op.Tasks) != 6 || len(op.Questions) != 1 || op.Questions[0].Status != bridge.QuestionAnswered || op.Questions[0].AnsweredAt == nil {
		t.Fatalf("operation=%+v error=%v", op, err)
	}
	var saved struct {
		Questions []struct {
			Answer string
			TaskID string `json:"task_id"`
		}
		Tasks []struct {
			ActionName string `json:"action_name"`
			Result     *struct {
				Output struct {
					BrokenLinks int `json:"broken_links"`
					Issues      []struct{ Check string }
					Indicators  []struct{ Name, Version, Source string }
				}
				Artifacts []json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(op.Raw, &saved); err != nil || len(saved.Questions) != 1 || saved.Questions[0].Answer != "Yes" || saved.Questions[0].TaskID == "" {
		t.Fatalf("linked operator answer was lost: %s, error=%v", op.Raw, err)
	}
	seoChecked, technologiesDetected := false, false
	for _, task := range saved.Tasks {
		if task.Result == nil || task.ActionName == "check-links" && task.Result.Output.BrokenLinks != 1 || task.ActionName == "capture-page" && len(task.Result.Artifacts) != 2 {
			t.Fatalf("website evidence missing: %+v", task)
		}
		if task.ActionName == "check-seo" {
			seoChecked = slices.ContainsFunc(task.Result.Output.Issues, func(issue struct{ Check string }) bool {
				return issue.Check == "missing-meta-description"
			})
		}
		if task.ActionName == "detect-technologies" {
			technologiesDetected = slices.ContainsFunc(task.Result.Output.Indicators, func(indicator struct{ Name, Version, Source string }) bool {
				return indicator.Name == "WordPress" && indicator.Version == "6.6.2" && indicator.Source == "meta-generator"
			})
		}
	}
	if !seoChecked || !technologiesDetected {
		t.Fatalf("missing new website evidence: seo=%t technologies=%t", seoChecked, technologiesDetected)
	}
	var report struct{ Output struct{ Summary string } }
	if err := json.Unmarshal(call("get_report", map[string]any{"operation_id": operationID}), &report); err != nil || report.Output.Summary != summary || asked.Load() != 1 || backgroundReviews.Load() != 1 {
		t.Fatalf("report=%+v error=%v questions=%d reviews=%d", report, err, asked.Load(), backgroundReviews.Load())
	}
}
