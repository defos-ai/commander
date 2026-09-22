package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"commander/apps/playground/internal/llm"
)

func sse(w http.ResponseWriter, value any) {
	data, _ := json.Marshal(value)
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

func TestCompatibleProvidersPreserveToolCallsAndSignatures(t *testing.T) {
	for _, provider := range []string{"gemini", "xai", "deepseek", "other"} {
		t.Run(provider, func(t *testing.T) {
			// Arrange: a streaming API returns fragmented arguments and vendor metadata.
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
				var body struct {
					Model    string
					Messages []map[string]json.RawMessage
					Tools    []json.RawMessage
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body.Model != "fixture" || len(body.Tools) != 1 {
					t.Errorf("request lost model or tools: %+v", body)
				}
				if requests.Add(1) == 1 {
					sse(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"role": "assistant", "content": "Checking ", "reasoning_content": "private reasoning", "tool_calls": []any{map[string]any{"index": 0, "id": "call-1", "type": "function", "extra_content": map[string]any{"google": map[string]string{"thought_signature": "opaque"}}, "function": map[string]string{"name": "list_missions", "arguments": "{"}}}}}}})
					sse(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "missions.", "tool_calls": []any{map[string]any{"index": 0, "function": map[string]string{"arguments": "}"}}}}, "finish_reason": "tool_calls"}}})
				} else {
					if len(body.Messages) != 4 || string(body.Messages[3]["tool_call_id"]) != `"call-1"` {
						t.Errorf("tool result not paired: %s", body.Messages)
					}
					assistant, _ := json.Marshal(body.Messages[2])
					if !strings.Contains(string(assistant), "thought_signature") || !strings.Contains(string(assistant), "private reasoning") {
						t.Errorf("provider metadata discarded: %s", assistant)
					}
					sse(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "One mission available."}, "finish_reason": "stop"}}})
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			client, err := llm.New(llm.Config{Provider: provider, BaseURL: server.URL + "/v1", Model: "fixture", APIKey: "test-key"})
			if err != nil {
				t.Fatal(err)
			}
			tools := []llm.Tool{{Name: "list_missions", Schema: json.RawMessage(`{"type":"object"}`)}}
			history := []llm.Message{{Role: "user", Text: "List missions"}}
			var streamed strings.Builder

			// Act: complete a model → tool → model round trip.
			reply, err := client.Complete(t.Context(), "system", history, tools, func(text string) { streamed.WriteString(text) })
			if err != nil {
				t.Fatal(err)
			}
			if len(reply.Calls) != 1 {
				t.Fatalf("missing call: %+v", reply)
			}
			history = append(history, reply, llm.Message{Role: "tool", CallID: reply.Calls[0].ID, Name: reply.Calls[0].Name, Text: `{"missions":[{"name":"review"}]}`})
			final, err := client.Complete(t.Context(), "system", history, tools, nil)

			// Assert: text streams separately; the complete call and native fields survive.
			if err != nil || final.Text != "One mission available." || streamed.String() != "Checking missions." || string(reply.Calls[0].Arguments) != "{}" {
				t.Fatalf("round trip: %+v %+v %q %v", reply, final, streamed.String(), err)
			}
		})
	}
}

func TestAnthropicToolResultsAreGrouped(t *testing.T) {
	// Arrange: two tool calls in a single Anthropic assistant message.
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") == "" {
			t.Error("missing Anthropic request fields")
		}
		var body struct {
			Messages []struct {
				Role    string
				Content json.RawMessage
			}
			System string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if requests.Add(1) == 1 {
			for index, name := range []string{"list_missions", "list_agents"} {
				sse(w, map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": fmt.Sprintf("call-%d", index), "name": name, "input": map[string]any{}}})
				sse(w, map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]string{"type": "input_json_delta", "partial_json": "{}"}})
			}
			sse(w, map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": "tool_use"}})
		} else {
			if len(body.Messages) != 3 || body.Messages[2].Role != "user" {
				t.Errorf("tool results were not grouped: %+v", body)
			}
			var content []map[string]any
			if err := json.Unmarshal(body.Messages[2].Content, &content); err != nil || len(content) != 2 {
				t.Errorf("results: %s", body.Messages[2].Content)
			}
			sse(w, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
			sse(w, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "Ready."}})
			sse(w, map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": "end_turn"}})
		}
		sse(w, map[string]string{"type": "message_stop"})
	}))
	defer server.Close()
	client, _ := llm.New(llm.Config{Provider: "anthropic", BaseURL: server.URL + "/v1", Model: "fixture", APIKey: "test-key"})
	history := []llm.Message{{Role: "user", Text: "Inspect Commander"}}

	// Act.
	reply, err := client.Complete(t.Context(), "system", history, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, reply)
	for _, call := range reply.Calls {
		history = append(history, llm.Message{Role: "tool", CallID: call.ID, Name: call.Name, Text: `{}`})
	}
	final, err := client.Complete(t.Context(), "system", history, nil, nil)

	// Assert.
	if err != nil || len(reply.Calls) != 2 || final.Text != "Ready." {
		t.Fatalf("round trip: %+v %+v %v", reply, final, err)
	}
}

func TestIncompleteAndFailedStreamsDoNotReturnExecutableCalls(t *testing.T) {
	for _, body := range []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"start_operation","arguments":"{}"}}]}}]}` + "\n\n",
		`data: {"error":{"message":"test-key"}}` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n",
	} {
		// Arrange.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		client, _ := llm.New(llm.Config{BaseURL: server.URL, Model: "fixture", APIKey: "test-key"})
		// Act.
		reply, err := client.Complete(t.Context(), "", nil, nil, nil)
		server.Close()
		// Assert.
		if err == nil || len(reply.Calls) != 0 || strings.Contains(err.Error(), "test-key") {
			t.Fatalf("unsafe partial reply: %+v %v", reply, err)
		}
	}
}

func TestModelDiscoveryAndErrors(t *testing.T) {
	// Arrange: discovery is separate from inference, and errors may echo secrets.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			fmt.Fprint(w, `{"data":[{"id":"model-b"},{"id":"model-a"}]}`)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"secret-key"}`)
	}))
	defer server.Close()
	client, _ := llm.New(llm.Config{BaseURL: server.URL, Model: "fixture", APIKey: "secret-key"})
	// Act.
	models, err := client.Models(t.Context())
	_, authErr := client.Complete(t.Context(), "", nil, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, cancelErr := client.Models(ctx)
	// Assert.
	if err != nil || strings.Join(models, ",") != "model-a,model-b" || authErr == nil || strings.Contains(authErr.Error(), "secret-key") || cancelErr == nil {
		t.Fatalf("models=%v err=%v auth=%v cancel=%v", models, err, authErr, cancelErr)
	}
}
