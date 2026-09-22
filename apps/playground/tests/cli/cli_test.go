package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"commander/apps/playground/cli"
	"commander/apps/playground/tests/fixture"
)

type output struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (o *output) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.data.Write(data)
}
func (o *output) String() string { o.mu.Lock(); defer o.mu.Unlock(); return o.data.String() }

func until(t *testing.T, out *output, text string) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !strings.Contains(out.String(), text) {
		select {
		case <-deadline.C:
			t.Fatalf("missing %q in terminal output:\n%s", text, out.String())
		case <-tick.C:
		}
	}
}
func submit(t *testing.T, input io.Writer, text string) {
	t.Helper()
	// Clear any prefilled input, type, then press Enter.
	if _, err := io.WriteString(input, "\x01\x0b"+text+"\r"); err != nil {
		t.Fatal(err)
	}
}

func press(t *testing.T, input io.Writer, keys string) {
	t.Helper()
	if _, err := io.WriteString(input, keys); err != nil {
		t.Fatal(err)
	}
}

func TestCommanderCLIChatThroughModelAndMCP(t *testing.T) {
	for _, provider := range []string{"other", "openai"} {
		t.Run(provider, func(t *testing.T) {
			// Arrange: real CLI and protocol clients, with only loopback model/MCP servers.
			t.Setenv("COMMANDER_LLM_API_KEY", "fixture-secret")
			t.Setenv("OPENAI_API_KEY", "fixture-secret")
			t.Setenv("COMMANDER_LLM_PROVIDER", "invalid-env-provider")
			t.Setenv("COMMANDER_LLM_BASE_URL", "invalid-env-url")
			t.Setenv("COMMANDER_MCP_URL", "invalid-env-mcp")
			var toolCalls atomic.Int32
			mcpURL := fixture.MCP(t, map[string]fixture.Tool{
				"list_missions": func(context.Context, json.RawMessage) (any, error) {
					toolCalls.Add(1)
					return map[string]any{"missions": []any{map[string]string{"name": "review"}}}, nil
				},
			})
			var requests atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := "/v1/chat/completions"
				if provider == "openai" {
					path = "/v1/responses"
				}
				if r.URL.Path != path || r.Header.Get("Authorization") != "Bearer fixture-secret" {
					t.Error("incorrect model connection")
				}
				var body struct {
					Messages []map[string]any
					Input    []map[string]any
					Model    string
					Tools    []map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body.Model != "fixture-chat" || len(body.Tools) < 1 {
					t.Error("model or MCP tool catalog missing")
				}
				if provider == "openai" {
					var output []any
					text := "Checking missions."
					if requests.Add(1) == 1 {
						output = []any{map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call-1", "name": "list_missions", "arguments": "{}", "status": "completed"}}
					} else {
						if len(body.Input) == 0 {
							t.Error("missing Responses input")
							return
						}
						last := body.Input[len(body.Input)-1]
						if last["type"] != "function_call_output" || last["call_id"] != "call-1" || !strings.Contains(fmt.Sprint(last["output"]), "review") {
							t.Error("MCP result was not returned to Responses")
						}
						text = "The review mission is available."
						output = []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": text}}}}
					}
					w.Header().Set("Content-Type", "text/event-stream")
					for _, event := range []any{
						map[string]any{"type": "response.output_text.delta", "delta": text},
						map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": output}},
					} {
						data, _ := json.Marshal(event)
						fmt.Fprintf(w, "data: %s\n\n", data)
						w.(http.Flusher).Flush()
					}
					return
				}
				if requests.Add(1) == 1 {
					fixture.SSE(w, map[string]any{"content": "Checking missions.", "tool_calls": []any{map[string]any{"index": 0, "id": "call-1", "type": "function", "function": map[string]string{"name": "list_missions", "arguments": "{}"}}}}, "tool_calls")
				} else {
					last := body.Messages[len(body.Messages)-1]
					if last["role"] != "tool" || !strings.Contains(fmt.Sprint(last["content"]), "review") {
						t.Error("MCP result was not returned to the model")
					}
					fixture.SSE(w, map[string]any{"content": "The review mission is available."}, "stop")
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer model.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			out := new(output)
			done := make(chan error, 1)
			go func() {
				done <- cli.Run(ctx, []string{"--provider=" + provider, "--model=fixture-chat", "--base-url=" + model.URL + "/v1", "--mcp-url=" + mcpURL, "--no-color"}, input, out, out)
			}()
			until(t, out, "Commander connected")

			// Act: submit chat, follow the streamed reply, then use help and exit.
			submit(t, writer, "List missions")
			until(t, out, "The review mission is available.")
			submit(t, writer, "/help")
			until(t, out, "/watch <operation-id>")
			submit(t, writer, "/exit")
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("CLI did not exit")
			}

			// Assert: actual MCP tool execution completed without exposing credentials.
			if toolCalls.Load() != 1 || requests.Load() != 2 || strings.Contains(out.String(), "fixture-secret") {
				t.Fatalf("calls=%d requests=%d secret leaked=%t", toolCalls.Load(), requests.Load(), strings.Contains(out.String(), "fixture-secret"))
			}
		})
	}
}

func TestCommanderSetupFallsBackToManualModelAndMasksKey(t *testing.T) {
	// Arrange: no discoverable models; all setup occurs through the real input loop.
	for _, name := range []string{"COMMANDER_DEBUG", "COMMANDER_LLM_PROVIDER", "COMMANDER_LLM_MODEL", "COMMANDER_LLM_BASE_URL", "COMMANDER_LLM_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "XAI_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Setenv(name, "")
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":[]}`) }))
	defer model.Close()
	mcpURL := fixture.MCP(t, map[string]fixture.Tool{})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	out := new(output)
	done := make(chan error, 1)
	go func() { done <- cli.Run(ctx, []string{"--mcp-url=" + mcpURL, "--no-color"}, input, out, out) }()

	// Act: choose Other, then recover from empty discovery with a URL and model name.
	until(t, out, "Select your model provider")
	press(t, writer, strings.Repeat("\x1b[B", 5)+"\r")
	until(t, out, "API base URL")
	submit(t, writer, model.URL)
	until(t, out, "Kept in memory")
	submit(t, writer, "typed-secret")
	until(t, out, "No models found")
	submit(t, writer, model.URL)
	until(t, out, "Exact model name")
	submit(t, writer, "custom-chat")
	until(t, out, "Commander connected")
	submit(t, writer, "/exit")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("CLI did not exit")
	}

	// Assert: setup supports manual endpoints and never renders the API key.
	if strings.Contains(out.String(), "typed-secret") || !strings.Contains(out.String(), "custom-chat") {
		t.Fatal("setup lost the model or exposed the API key")
	}
}

func TestCommanderSetupSelectsProviderAndModelWithArrowKeys(t *testing.T) {
	for _, test := range []struct {
		name, keys, model string
		manual            bool
	}{
		{"scroll to a discovered model", strings.Repeat("\x1b[B", 26) + "\x1b[A", "model-25", false},
		{"enter an unlisted model", "\x1b[F", "private-chat", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: a long model list, no saved provider, and local protocol servers.
			for _, name := range []string{"COMMANDER_DEBUG", "COMMANDER_LLM_PROVIDER", "COMMANDER_LLM_MODEL", "COMMANDER_LLM_BASE_URL", "COMMANDER_LLM_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "XAI_API_KEY", "DEEPSEEK_API_KEY"} {
				t.Setenv(name, "")
			}
			selected := make(chan string, 1)
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					var models []map[string]string
					for i := range 40 {
						models = append(models, map[string]string{"id": fmt.Sprintf("model-%02d", i)})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": models})
					return
				}
				var body struct{ Model string }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				selected <- body.Model
				fixture.SSE(w, map[string]any{"content": "Selected model connected."}, "stop")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer model.Close()
			mcpURL := fixture.MCP(t, map[string]fixture.Tool{})
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			out := new(output)
			done := make(chan error, 1)
			go func() {
				done <- cli.Run(ctx, []string{"--base-url=" + model.URL, "--mcp-url=" + mcpURL, "--no-color"}, input, out, out)
			}()

			// Act: use arrow keys to choose Other and navigate beyond the first model page.
			until(t, out, "DEFOS | Commander Playground")
			press(t, writer, strings.Repeat("\x1b[B", 5)+"\x1b[A\x1b[B\r")
			until(t, out, "Kept in memory")
			press(t, writer, "\r")
			until(t, out, "Select a model:")
			press(t, writer, test.keys)
			if test.manual {
				until(t, out, "› Enter model name…")
			} else {
				until(t, out, "› "+test.model)
			}
			press(t, writer, "\r")
			if test.manual {
				until(t, out, "Exact model name")
				submit(t, writer, test.model)
			}
			until(t, out, "Commander connected")
			submit(t, writer, "Hello")
			until(t, out, "Selected model connected.")
			submit(t, writer, "/exit")

			// Assert: selection reaches the actual model request and exits normally.
			select {
			case got := <-selected:
				if got != test.model {
					t.Fatalf("selected model = %q, want %q", got, test.model)
				}
			case <-ctx.Done():
				t.Fatal("model was not called")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("CLI did not exit")
			}
		})
	}
}

func TestCommanderHelpAndConfigurationErrors(t *testing.T) {
	for _, test := range []struct {
		args   []string
		want   string
		failed bool
	}{
		{[]string{"--help"}, "Usage: commander", false},
		{[]string{"--provider=missing"}, "unknown provider", true},
		{[]string{"--mcp-url=file:///state"}, "mcp-url", true},
	} {
		// Arrange.
		var output bytes.Buffer
		// Act.
		err := cli.Run(t.Context(), test.args, strings.NewReader(""), &output, &output)
		// Assert.
		text := output.String()
		if err != nil {
			text += err.Error()
		}
		if (err != nil) != test.failed || !strings.Contains(text, test.want) {
			t.Fatalf("args=%v output=%q err=%v", test.args, text, err)
		}
	}
}

func TestCommanderExitCancelsInFlightModel(t *testing.T) {
	// Arrange: a model streams a prefix, then stays busy until the request is cancelled.
	t.Setenv("COMMANDER_LLM_API_KEY", "fixture-secret")
	mcpURL := fixture.MCP(t, map[string]fixture.Tool{})
	cancelled := make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.SSE(w, map[string]any{"content": "Still working"}, "")
		<-r.Context().Done()
		close(cancelled)
	}))
	defer model.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	out := new(output)
	done := make(chan error, 1)
	go func() {
		done <- cli.Run(ctx, []string{"--provider=other", "--model=fixture", "--base-url=" + model.URL, "--mcp-url=" + mcpURL, "--no-color"}, input, out, out)
	}()
	until(t, out, "Commander connected")

	// Act: exit while the model is still streaming.
	submit(t, writer, "Start a reply")
	until(t, out, "Still working")
	submit(t, writer, "/exit")

	// Assert: input remained responsive and shutdown cancelled the outstanding request.
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("shutdown blocked on model")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("model request survived exit")
	}
}
