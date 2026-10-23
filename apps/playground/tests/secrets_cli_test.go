package playground_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/playground/cli"
)

func TestSecretAPIURLValidation(t *testing.T) {
	for _, name := range []string{"COMMANDER_API_URL", "COMMANDER_API_USERNAME", "COMMANDER_API_PASSWORD", "COMMANDER_MCP_TOKEN", "COMMANDER_ADMIN_URL", "COMMANDER_LLM_PROVIDER", "COMMANDER_LLM_BASE_URL"} {
		t.Setenv(name, "")
	}
	t.Setenv("COMMANDER_MCP_URL", "http://localhost:4273/mcp")
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Configuration must validate before starting a terminal or hosts.
	for _, endpoint := range []string{"http://localhost:4274", "https://api.example.test/commander", ""} {
		if err := cli.Run(ctx, []string{"--api-url", endpoint}, strings.NewReader(""), io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
			t.Fatalf("valid API endpoint rejected: %v", err)
		}
	}
	for _, endpoint := range []string{"file:///tmp/private-marker", "https://user:private-marker@api.example.test", "https://api.example.test?token=private-marker", "https://api.example.test/#private-marker"} {
		err := cli.Run(ctx, []string{"--api-url", endpoint}, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "api-url") || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("invalid API endpoint was accepted or exposed its contents")
		}
	}
	t.Setenv("COMMANDER_API_URL", "https://user:private-marker@api.example.test")
	if err := cli.Run(ctx, nil, strings.NewReader(""), io.Discard, io.Discard); err == nil || errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "private-marker") {
		t.Fatal("API URL environment value bypassed validation or leaked")
	}
	if err := cli.Run(ctx, []string{"--api-url=http://localhost:4274"}, strings.NewReader(""), io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal("explicit API endpoint did not override the environment")
	}
	if err := cli.Run(ctx, []string{"--all-in-one"}, strings.NewReader(""), io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal("all-in-one should ignore the remote API endpoint")
	}
	if err := cli.Run(ctx, []string{"--all-in-one", "--api-url=http://localhost:4274"}, strings.NewReader(""), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "--all-in-one starts its own API") {
		t.Fatal("explicit remote API endpoint was accepted in all-in-one mode")
	}
}
