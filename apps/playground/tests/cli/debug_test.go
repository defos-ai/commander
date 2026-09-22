package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"commander/apps/playground/cli"
	"commander/apps/playground/tests/fixture"
)

func TestCommanderDebugAndPrefixedEnvironment(t *testing.T) {
	for _, test := range []struct {
		name, env string
		flags     []string
		debug     bool
	}{
		{"default", "", nil, false},
		{"flag", "", []string{"--debug"}, true},
		{"environment", "true", nil, true},
		{"flag disables environment", "true", []string{"--debug=false"}, false},
		{"flag overrides invalid environment", "invalid", []string{"--debug"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: all connection settings come from COMMANDER_* variables.
			mcpURL := fixture.MCP(t, map[string]fixture.Tool{})
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct{ Model string }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "fixture" || r.Header.Get("Authorization") != "Bearer fixture-secret" {
					t.Error("prefixed model configuration was not used")
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"Unsupported model; fixture-secret","param":"model","code":"model_not_found"}}`)
			}))
			defer model.Close()
			for key, value := range map[string]string{
				"COMMANDER_MCP_URL": mcpURL, "COMMANDER_LLM_PROVIDER": "other",
				"COMMANDER_LLM_MODEL": "fixture", "COMMANDER_LLM_BASE_URL": model.URL,
				"COMMANDER_LLM_API_KEY": "fixture-secret", "COMMANDER_DEBUG": test.env,
			} {
				t.Setenv(key, value)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			out := new(output)
			done := make(chan error, 1)
			go func() { done <- cli.Run(ctx, append([]string{"--no-color"}, test.flags...), input, out, out) }()
			until(t, out, "Commander connected")

			// Act: encounter a model error and exit through the real UI.
			submit(t, writer, "Hello")
			if test.debug {
				until(t, out, "model_not_found")
			} else {
				until(t, out, "--debug")
			}
			submit(t, writer, "/exit")
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("CLI did not exit")
			}

			// Assert: details are opt-in, flags win, and credentials stay hidden.
			text := out.String()
			if strings.Contains(text, "fixture-secret") || strings.Contains(text, "model_not_found") != test.debug || strings.Contains(text, "--debug") == test.debug {
				t.Fatalf("unexpected debug output: %s", text)
			}
		})
	}
}

func TestCommanderRejectsInvalidDebugEnvironment(t *testing.T) {
	// Arrange.
	t.Setenv("COMMANDER_DEBUG", "invalid")
	var out strings.Builder
	// Act.
	err := cli.Run(t.Context(), nil, strings.NewReader(""), &out, &out)
	// Assert.
	if err == nil || !strings.Contains(err.Error(), "COMMANDER_DEBUG must be a boolean") {
		t.Fatalf("invalid debug configuration: %v", err)
	}
}
