package llm_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"commander/apps/playground/internal/llm"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func fixtureClient(t *testing.T, provider, body string) *llm.Client {
	t.Helper()
	client, err := llm.New(llm.Config{
		Provider: provider, BaseURL: "https://model.example", Model: "fixture",
		HTTPClient: &http.Client{Transport: fixtureTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// Build protocol fixtures with identical call semantics and optional completion.
func callStream(provider string, calls []llm.Call, complete bool) string {
	var body strings.Builder
	emit := func(value any) {
		data, _ := json.Marshal(value)
		fmt.Fprintf(&body, "data: %s\n\n", data)
	}
	switch provider {
	case "openai":
		var output []any
		for _, call := range calls {
			item := map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)}
			output = append(output, item)
			emit(map[string]any{"type": "response.output_item.done", "item": item})
		}
		if complete {
			emit(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": output}})
		}
	case "anthropic":
		for i, call := range calls {
			emit(map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": map[string]any{}}})
			emit(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(call.Arguments)}})
		}
		if complete {
			emit(map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": "tool_use"}})
			emit(map[string]any{"type": "message_stop"})
		}
	default:
		for i, call := range calls {
			emit(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": call.ID, "type": "function", "function": map[string]string{"name": call.Name, "arguments": string(call.Arguments)}}}}}}})
		}
		if complete {
			emit(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}}})
		}
	}
	return body.String()
}

func TestEveryProtocolValidatesCompleteToolCalls(t *testing.T) {
	valid := llm.Call{ID: "call-1", Name: "list_missions", Arguments: json.RawMessage(`{}`)}
	for _, provider := range []string{"openai", "anthropic", "other"} {
		for _, test := range []struct {
			name string
			call llm.Call
		}{
			{"null arguments", llm.Call{ID: "call-2", Name: valid.Name, Arguments: json.RawMessage(`null`)}},
			{"array arguments", llm.Call{ID: "call-2", Name: valid.Name, Arguments: json.RawMessage(`[]`)}},
			{"string arguments", llm.Call{ID: "call-2", Name: valid.Name, Arguments: json.RawMessage(`"{}"`)}},
			{"number arguments", llm.Call{ID: "call-2", Name: valid.Name, Arguments: json.RawMessage(`42`)}},
			{"broken arguments", llm.Call{ID: "call-2", Name: valid.Name, Arguments: json.RawMessage(`{`)}},
			{"missing ID", llm.Call{Name: valid.Name, Arguments: valid.Arguments}},
			{"blank name", llm.Call{ID: "call-2", Name: " \t", Arguments: valid.Arguments}},
			{"duplicate ID", valid},
		} {
			t.Run(provider+"/"+test.name, func(t *testing.T) {
				// A valid call preceding an invalid one must not escape for execution.
				client := fixtureClient(t, provider, callStream(provider, []llm.Call{valid, test.call}, true))
				reply, err := client.Complete(t.Context(), "", nil, nil, nil)
				if err == nil || len(reply.Calls) != 0 || len(reply.Native) != 0 {
					t.Fatalf("invalid call escaped: reply=%+v error=%v", reply, err)
				}
			})
		}
		t.Run(provider+"/object arguments retain numbers", func(t *testing.T) {
			call := valid
			call.Arguments = json.RawMessage(`{"count":9007199254740993,"options":{"enabled":true}}`)
			client := fixtureClient(t, provider, callStream(provider, []llm.Call{call}, true))
			reply, err := client.Complete(t.Context(), "", nil, nil, nil)
			if err != nil || len(reply.Calls) != 1 || string(reply.Calls[0].Arguments) != string(call.Arguments) || !strings.Contains(string(reply.Native), "9007199254740993") {
				t.Fatalf("arguments changed: reply=%+v error=%v", reply, err)
			}
		})
		t.Run(provider+"/incomplete stream", func(t *testing.T) {
			client := fixtureClient(t, provider, callStream(provider, []llm.Call{valid}, false))
			reply, err := client.Complete(t.Context(), "", nil, nil, nil)
			if err == nil || len(reply.Calls) != 0 || len(reply.Native) != 0 {
				t.Fatalf("incomplete call escaped: reply=%+v error=%v", reply, err)
			}
		})
	}
}

func TestAnthropicInitialToolInputMustBeAnObject(t *testing.T) {
	// A tool can have initial input without sending input_json_delta events.
	body := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call-1\",\"name\":\"list_missions\",\"input\":[]}}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	client := fixtureClient(t, "anthropic", body)
	reply, err := client.Complete(t.Context(), "", nil, nil, nil)
	if err == nil || len(reply.Calls) != 0 {
		t.Fatalf("non-object initial input escaped: reply=%+v error=%v", reply, err)
	}
}

func TestEveryProtocolBoundsStreamEventsAndResponses(t *testing.T) {
	// Every individual line is small enough for the former line-only limit.
	var multiline strings.Builder
	multiline.WriteString("data: {\n")
	for range 65 {
		fmt.Fprintf(&multiline, "data: \"padding\":\"%s\",\n", strings.Repeat("x", 64<<10))
	}
	multiline.WriteString("data: \"end\":true}\n\n")
	// Comments do not contribute model text, but still consume the response budget.
	response := strings.Repeat(":"+strings.Repeat("x", 64<<10)+"\n\n", 257)
	// CRLF bytes count even though the line scanner removes them from its tokens.
	crlfEvent := strings.Repeat(":"+strings.Repeat("x", 1021)+"\r\n", 4096) + "\r\n"
	for _, provider := range []string{"openai", "anthropic", "other"} {
		for _, test := range []struct{ name, suffix, want string }{
			{"multiline event", multiline.String(), "event exceeds 4 MiB"},
			{"CRLF event", crlfEvent, "event exceeds 4 MiB"},
			{"total response", response, "response exceeds 16 MiB"},
		} {
			t.Run(provider+"/"+test.name, func(t *testing.T) {
				// Even an earlier completion must not return calls after a stream error.
				call := llm.Call{ID: "call-1", Name: "list_missions", Arguments: json.RawMessage(`{}`)}
				client := fixtureClient(t, provider, callStream(provider, []llm.Call{call}, true)+test.suffix)
				reply, err := client.Complete(t.Context(), "", nil, nil, nil)
				if err == nil || !strings.Contains(err.Error(), test.want) || len(reply.Calls) != 0 || len(reply.Native) != 0 {
					t.Fatalf("oversized stream escaped: reply=%+v error=%v", reply, err)
				}
			})
		}
	}
}

func TestMultilineStreamEventWithinLimits(t *testing.T) {
	// A legal multiline event and CRLF separators still produce a complete reply.
	body := "event: message\r\n: keepalive\r\ndata: {\"choices\":[\r\ndata: {\"delta\":{\"content\":\"Ready.\"},\"finish_reason\":\"stop\"}]}\r\n\r\n"
	client := fixtureClient(t, "other", body)
	reply, err := client.Complete(t.Context(), "", nil, nil, nil)
	if err != nil || reply.Text != "Ready." {
		t.Fatalf("multiline reply=%+v error=%v", reply, err)
	}
}
