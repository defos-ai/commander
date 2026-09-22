package llm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"commander/apps/playground/internal/llm"
)

func TestProviderErrorsDiscardPartialCalls(t *testing.T) {
	for _, debug := range []bool{false, true} {
		for _, tc := range []struct {
			name, body string
			status     int
		}{
			{"HTTP", `{"error":{"code":"invalid_request","message":"secret-key"}}`, 400},
			{"nested stream", `{"type":"error","error":{"code":"invalid_request","message":"secret-key"}}`, 200},
			{"flat stream", `{"type":"error","code":"invalid_request","message":"secret-key"}`, 200},
			{"failed response", `{"type":"response.failed","response":{"status":"failed","error":{"code":"invalid_request","message":"secret-key"}}}`, 200},
		} {
			t.Run(fmt.Sprintf("%s/debug=%t", tc.name, debug), func(t *testing.T) {
				// Arrange: provider errors can arrive before or during a tool-call stream.
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tc.status != http.StatusOK {
						w.WriteHeader(tc.status)
						fmt.Fprint(w, tc.body)
						return
					}
					sse(w, map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "function_call", "name": "assign_task", "call_id": "call-1", "arguments": "{}"}})
					fmt.Fprintf(w, "data: %s\n\n", tc.body)
				}))
				defer server.Close()
				client, err := llm.New(llm.Config{Provider: "openai", BaseURL: server.URL, Model: "fixture", APIKey: "secret-key", Debug: debug})
				if err != nil {
					t.Fatal(err)
				}

				// Act.
				reply, err := client.Complete(t.Context(), "", nil, nil, nil)

				// Assert: failed streams never dispatch partial calls; only debug shows details.
				if err == nil || len(reply.Calls) != 0 || strings.Contains(err.Error(), "secret-key") {
					t.Fatalf("reply=%+v error=%v", reply, err)
				}
				if strings.Contains(err.Error(), "invalid_request") != debug || strings.Contains(err.Error(), "--debug") == debug {
					t.Fatalf("debug=%t error=%v", debug, err)
				}
			})
		}
	}
}

func TestDebugProviderErrorsAreRedactedAndBounded(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
	}{
		{"JSON", `{"error":{"message":"Bad parameter; secret-key","param":"tools","code":"invalid_schema"},"authorization":"another-secret"}`, "invalid_schema"},
		{"escaped key", `{"error":{"message":"Bad secret\u002dkey"}}`, "[REDACTED]"},
		{"plain text", "Bad request: secret-key\x1b[2J", "Bad request"},
		{"long body", strings.Repeat("x", 4090) + "secret-key" + strings.Repeat("x", 500), "[truncated]"},
		{"oversized body", strings.Repeat("x", 65532) + "secret-key", "exceeds 64 KiB"},
		{"empty body", "", "no error details"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: gateways may return JSON, text, echoed credentials or huge bodies.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			client, err := llm.New(llm.Config{BaseURL: server.URL, Model: "fixture", APIKey: "secret-key", Debug: true})
			if err != nil {
				t.Fatal(err)
			}
			// Act: both discovery and inference use the same diagnostics.
			_, discoveryErr := client.Models(t.Context())
			_, inferenceErr := client.Complete(t.Context(), "", nil, nil, nil)
			// Assert.
			for _, err := range []error{discoveryErr, inferenceErr} {
				if err == nil {
					t.Fatal("missing provider error")
				}
				text := err.Error()
				if !strings.Contains(text, test.want) || strings.Contains(text, "secret-key") || strings.Contains(text, "another-secret") || strings.Contains(text, "\x1b") || len(text) > 4500 {
					t.Fatalf("unsafe or incomplete diagnostic: %s", text)
				}
			}
		})
	}
}

func TestDebugStreamingErrorsPreserveProviderDetails(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic", "other"} {
		for _, debug := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/debug=%t", provider, debug), func(t *testing.T) {
				// Arrange: the provider sends an error event after a successful HTTP response.
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if provider == "openai" {
						sse(w, map[string]any{"type": "error", "message": "secret-key", "code": "overloaded"})
					} else {
						sse(w, map[string]any{"type": "error", "error": map[string]string{"message": "secret-key", "code": "overloaded"}})
					}
				}))
				defer server.Close()
				client, err := llm.New(llm.Config{Provider: provider, BaseURL: server.URL, Model: "fixture", APIKey: "secret-key", Debug: debug})
				if err != nil {
					t.Fatal(err)
				}
				// Act.
				reply, err := client.Complete(t.Context(), "", []llm.Message{{Role: "user", Text: "Hello"}}, []llm.Tool{{Name: "example", Schema: json.RawMessage(`{"type":"object"}`)}}, nil)
				// Assert: no actions escape a failed stream; details follow debug mode.
				if err == nil || len(reply.Calls) != 0 || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "overloaded") != debug || strings.Contains(err.Error(), "--debug") == debug {
					t.Fatalf("reply=%+v error=%v", reply, err)
				}
			})
		}
	}
}
