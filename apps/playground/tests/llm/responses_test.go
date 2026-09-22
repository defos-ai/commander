package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"commander/apps/playground/internal/llm"
)

func TestOpenAIResponsesPreserveReasoningAndToolResults(t *testing.T) {
	// Arrange: reasoning, commentary and two calls must survive the next request.
	output := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque-context"}`),
		json.RawMessage(`{"type":"message","id":"msg_1","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"Checking missions.","annotations":[]}]}`),
		json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call-1","name":"list_missions","arguments":"{}","status":"completed"}`),
		json.RawMessage(`{"type":"function_call","id":"fc_2","call_id":"call-2","name":"list_agents","arguments":"{}","status":"completed"}`),
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if string(body["model"]) != `"fixture"` || string(body["instructions"]) != `"system"` || string(body["stream"]) != "true" || string(body["store"]) != "false" || string(body["include"]) != `["reasoning.encrypted_content"]` {
			t.Errorf("missing Responses configuration: %s", body)
		}
		if body["reasoning"] != nil || body["reasoning_effort"] != nil || body["messages"] != nil {
			t.Error("request overrides reasoning or includes Chat Completions fields")
		}
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(body["tools"], &tools); err != nil || len(tools) != 2 {
			t.Errorf("missing tools: %s", body["tools"])
			return
		}
		if string(tools[0]["name"]) != `"list_missions"` || string(tools[0]["strict"]) != "false" || string(tools[0]["parameters"]) != `{"type":"object","properties":{"optional":{"type":"string"}}}` || tools[0]["function"] != nil {
			t.Errorf("MCP schema changed or function incorrectly nested: %s", tools[0])
		}
		var input []map[string]any
		if err := json.Unmarshal(body["input"], &input); err != nil {
			t.Error(err)
			return
		}
		if requests.Add(1) == 1 {
			if len(input) != 1 || input[0]["content"] != "Inspect Commander" {
				t.Errorf("lost user message: %+v", input)
			}
			sse(w, map[string]any{"type": "response.output_text.delta", "delta": "Checking "})
			sse(w, map[string]any{"type": "response.output_text.delta", "delta": "missions."})
			sse(w, map[string]any{"type": "response.function_call_arguments.delta", "delta": "{"})
			sse(w, map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": output}})
			return
		}
		if len(input) != 7 {
			t.Errorf("lost history items: %+v", input)
			return
		}
		for i, raw := range output {
			var want map[string]any
			_ = json.Unmarshal(raw, &want)
			if !reflect.DeepEqual(input[i+1], want) {
				t.Errorf("output item %d was not replayed intact: %+v", i, input[i+1])
			}
		}
		for i := range 2 {
			item := input[5+i]
			if item["type"] != "function_call_output" || item["call_id"] != fmt.Sprintf("call-%d", i+1) || item["output"] != "{}" {
				t.Errorf("unpaired tool result: %+v", item)
			}
		}
		sse(w, map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "Ready."}}}}}})
	}))
	defer server.Close()
	client, _ := llm.New(llm.Config{Provider: "openai", BaseURL: server.URL + "/v1", Model: "fixture", APIKey: "test-key"})
	tools := []llm.Tool{
		{Name: "list_missions", Schema: json.RawMessage(`{"type":"object","properties":{"optional":{"type":"string"}}}`)},
		{Name: "list_agents", Schema: json.RawMessage(`{"type":"object"}`)},
	}
	history := []llm.Message{{Role: "user", Text: "Inspect Commander"}}
	var streamed strings.Builder

	// Act: feed actual completed output and both tool results back to the model.
	reply, err := client.Complete(t.Context(), "system", history, tools, func(text string) { streamed.WriteString(text) })
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, reply)
	for _, call := range reply.Calls {
		history = append(history, llm.Message{Role: "tool", CallID: call.ID, Text: "{}"})
	}
	final, err := client.Complete(t.Context(), "system", history, tools, nil)

	// Assert: call_id (not item ID) pairs results; opaque reasoning isn't rendered.
	if err != nil || final.Text != "Ready." || reply.Text != "Checking missions." || streamed.String() != reply.Text || len(reply.Calls) != 2 || requests.Load() != 2 {
		t.Fatalf("round trip: reply=%+v final=%+v streamed=%q error=%v", reply, final, streamed.String(), err)
	}
}

func TestOpenAIIncompleteResponsesNeverReturnCalls(t *testing.T) {
	call := `{"type":"function_call","id":"fc_1","call_id":"call-1","name":"start_operation","arguments":"{}"}`
	for _, test := range []struct{ name, event string }{
		{"disconnected", ""},
		{"failed", `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"test-key"}}}`},
		{"incomplete", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`},
		{"malformed event", `{`},
		{"inconsistent status", `{"type":"response.completed","response":{"status":"incomplete","output":[` + call + `]}}`},
		{"missing output", `{"type":"response.completed","response":{"status":"completed"}}`},
		{"duplicate calls", `{"type":"response.completed","response":{"status":"completed","output":[` + call + `,` + call + `]}}`},
		{"missing call ID", `{"type":"response.completed","response":{"status":"completed","output":[` + strings.Replace(call, `"call_id":"call-1",`, "", 1) + `]}}`},
		{"broken arguments", `{"type":"response.completed","response":{"status":"completed","output":[` + strings.Replace(call, `"arguments":"{}"`, `"arguments":"{"`, 1) + `]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: even a finished call item is not a completed response.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				sse(w, map[string]any{"type": "response.output_item.done", "item": json.RawMessage(call)})
				fmt.Fprintf(w, "data: %s\n\n", test.event)
			}))
			defer server.Close()
			client, _ := llm.New(llm.Config{Provider: "openai", BaseURL: server.URL, Model: "fixture", APIKey: "test-key", Debug: true})
			// Act.
			reply, err := client.Complete(t.Context(), "", nil, nil, nil)
			// Assert.
			if err == nil || len(reply.Calls) != 0 || strings.Contains(err.Error(), "test-key") {
				t.Fatalf("unsafe partial reply: %+v %v", reply, err)
			}
		})
	}
}

func TestOpenAIResponseCanBeCanceledWhileStreaming(t *testing.T) {
	// Arrange: the server streams text, then waits without completing the call.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w, map[string]any{"type": "response.output_text.delta", "delta": "Working"})
		<-r.Context().Done()
	}))
	defer server.Close()
	client, _ := llm.New(llm.Config{Provider: "openai", BaseURL: server.URL, Model: "fixture"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Act: cancel as soon as the first text arrives.
	reply, err := client.Complete(ctx, "", nil, nil, func(string) { cancel() })
	// Assert.
	if !errors.Is(err, context.Canceled) || len(reply.Calls) != 0 {
		t.Fatalf("reply=%+v cancellation=%v", reply, err)
	}
}
