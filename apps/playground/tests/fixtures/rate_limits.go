package fixtures

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
)

const RateLimitProviderKey = "fixture-rate-limit-provider-key"

const CompletedRateLimitStream = "data: " + `{"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","id":"rs_fixture","encrypted_content":"opaque-fixture"},{"type":"function_call","call_id":"fixture-call","name":"list_agents","arguments":"{}"}]}}` + "\n\n"

func RateLimitBody(code string, headers map[string]string) string {
	body, _ := json.Marshal(map[string]any{"error": rateLimitFailure(code, headers)})
	return string(body)
}

func RateLimitStream(kind, code string, headers map[string]string) string {
	event := map[string]any{"type": kind, "error": rateLimitFailure(code, headers)}
	if kind == "response.failed" {
		delete(event, "error")
		event["response"] = map[string]any{"error": rateLimitFailure(code, headers)}
	}
	body, _ := json.Marshal(event)
	return "data: " + string(body) + "\n\n"
}

func rateLimitFailure(code string, headers map[string]string) map[string]any {
	return map[string]any{
		"code": code, "type": "tokens", "headers": headers,
		"message": "provider-diagnostic-only " + RateLimitProviderKey,
	}
}

func NewRateLimitClient(t *testing.T, provider *fakes.RateLimitProvider, debug bool) *llm.Client {
	t.Helper()
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)
	client, err := llm.New(llm.Config{
		Provider: "openai", BaseURL: server.URL, Model: "fixture-model",
		APIKey: RateLimitProviderKey, Debug: debug,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
