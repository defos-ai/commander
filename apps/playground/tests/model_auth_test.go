package playground_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
)

func TestModelAuthenticationErrors(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			t.Run(fmt.Sprintf("%s/%d", provider, status), func(t *testing.T) {
				var requests atomic.Int32
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.URL.Path != "/models" {
						t.Errorf("unexpected request path: %s", r.URL.Path)
					}
					if provider == "anthropic" {
						if r.Header.Get("x-api-key") != "fixture-key" {
							t.Error("Anthropic request has no API key")
						}
					} else if r.Header.Get("Authorization") != "Bearer fixture-key" {
						t.Error("model request has no Bearer API key")
					}
					http.Error(w, "rejected", status)
				}))
				defer endpoint.Close()
				client, err := llm.New(llm.Config{Provider: provider, BaseURL: endpoint.URL, APIKey: "fixture-key"})
				if err != nil {
					t.Fatal(err)
				}
				_, err = client.Models(t.Context())
				var authentication *llm.AuthenticationError
				if !errors.As(err, &authentication) {
					t.Fatalf("expected typed authentication error, got %T: %v", err, err)
				}
				if !strings.Contains(err.Error(), "check your API key and model access") {
					t.Fatalf("missing authentication hint: %v", err)
				}
				if requests.Load() != 1 {
					t.Fatalf("authentication failure retried automatically: %d requests", requests.Load())
				}
			})
		}
	}
}

func TestModelAuthenticationErrorClassification(t *testing.T) {
	const invalidGeminiKey = `{"error":{"code":400,"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID"}]}}`
	for _, tc := range []struct {
		name, provider, body string
		status               int
		authentication       bool
	}{
		{"Gemini invalid key", "gemini", invalidGeminiKey, 400, true},
		{"unrelated Gemini reason", "gemini", strings.ReplaceAll(invalidGeminiKey, "API_KEY_INVALID", "INVALID_ARGUMENT"), 400, false},
		{"unrelated Gemini detail", "gemini", strings.ReplaceAll(invalidGeminiKey, "google.rpc.ErrorInfo", "google.rpc.LocalizedMessage"), 400, false},
		{"unstructured Gemini body", "gemini", "API_KEY_INVALID", 400, false},
		{"invalid Gemini JSON", "gemini", invalidGeminiKey + " garbage", 400, false},
		{"other provider bad request", "other", invalidGeminiKey, 400, false},
		{"unsupported model discovery", "openai", "not found", 404, false},
		{"rate limited", "openai", "quota exceeded", 429, false},
		{"endpoint error", "openai", "unavailable", 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer endpoint.Close()
			client, err := llm.New(llm.Config{Provider: tc.provider, BaseURL: endpoint.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Models(t.Context())
			if err == nil {
				t.Fatal("error response was accepted")
			}
			var authentication *llm.AuthenticationError
			if got := errors.As(err, &authentication); got != tc.authentication {
				t.Fatalf("authentication classification = %t, want %t: %v", got, tc.authentication, err)
			}
		})
	}
}

func TestModelAuthenticationErrorDiagnostics(t *testing.T) {
	const key = "fixture-secret-key"
	body, err := json.Marshal(map[string]any{"error": map[string]string{"message": "invalid " + key, "api_key": key}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body, detail string
		debug              bool
	}{
		{"normal", string(body), "rerun with --debug", false},
		{"debug", string(body), "[REDACTED]", true},
		{"oversized body", strings.Repeat("x", 64*1024-5) + key, "provider error body exceeds 64 KiB", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, tc.body)
			}))
			defer endpoint.Close()
			client, err := llm.New(llm.Config{Provider: "openai", BaseURL: endpoint.URL, APIKey: key, Debug: tc.debug})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Models(t.Context())
			var authentication *llm.AuthenticationError
			if !errors.As(err, &authentication) {
				t.Fatalf("expected typed authentication error, got %T", err)
			}
			if strings.Contains(err.Error(), key) || strings.Contains(errors.Unwrap(err).Error(), key) {
				t.Fatal("authentication error leaked API key")
			}
			if !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("missing safe diagnostic %q", tc.detail)
			}
			if errors.Unwrap(err).Error() != err.Error() {
				t.Fatal("typed authentication error changed diagnostic text")
			}
		})
	}
}
