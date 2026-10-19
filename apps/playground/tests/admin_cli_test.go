package playground_test

import (
	"context"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/cli"
)

func TestAdminURLValidation(t *testing.T) {
	t.Setenv("COMMANDER_MCP_URL", "http://localhost:4273/mcp")
	t.Setenv("COMMANDER_MCP_TOKEN", "")
	t.Setenv("COMMANDER_ADMIN_URL", "")
	t.Setenv("COMMANDER_LLM_PROVIDER", "")
	t.Setenv("COMMANDER_LLM_BASE_URL", "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Validate flags without starting a terminal or any hosts.
	for _, endpoint := range []string{
		"http://localhost:4275", "https://admin.example.test/tools/commander/", "http://[::1]:4275",
	} {
		err := cli.Run(ctx, []string{"--admin-url", endpoint}, strings.NewReader(""), io.Discard, io.Discard)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("valid browser address rejected: %v", err)
		}
	}
	for _, endpoint := range []string{
		"//admin.example.test", "file:///tmp/ui", "javascript:alert(1)", "http://0.0.0.0:4275", "http://[::]:4275",
		"https://user:private-marker@admin.example.test", "https://admin.example.test?token=private-marker",
		"https://admin.example.test?", "https://admin.example.test#", "https://admin.example.test/#/connect",
		"https://admin.example.test:0", "https://admin.example.test:65536", "https://admin.example.test:",
		"https://admin.example.test/\nprivate-marker", "https://admin.example.test/%1bprivate-marker", "https://admin.example.test\\private-marker",
	} {
		err := cli.Run(ctx, []string{"--admin-url", endpoint}, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "admin-url") || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("unsafe Admin address was accepted or echoed into the error")
		}
	}
	t.Setenv("COMMANDER_ADMIN_URL", "https://user:private-marker@admin.example.test")
	err := cli.Run(ctx, nil, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "private-marker") {
		t.Fatal("Admin URL environment value bypassed validation")
	}
	err = cli.Run(ctx, []string{"--admin-url=https://admin.example.test"}, strings.NewReader(""), io.Discard, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("explicit Admin URL did not override the environment")
	}
	err = cli.Run(ctx, []string{"--all-in-one", "--no-admin", "--admin-url=https://admin.example.test"}, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--no-admin cannot") {
		t.Fatal("disabled Admin accepted an advertised URL")
	}
}

func TestRemoteAdminAddress(t *testing.T) {
	t.Setenv("COMMANDER_ADMIN_URL", "https://admin.example.test/commander/")
	t.Setenv("COMMANDER_MCP_URL", "http://localhost:4273/mcp")
	t.Setenv("COMMANDER_MCP_TOKEN", "")
	output := lookupAdmin(t, nil, "Admin: https://admin.example.test/commander/")
	if strings.Contains(output, "#/connect") {
		t.Fatal("remote Playground inferred API topology from the MCP address")
	}
}

func testAllInOneAdminAddress(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, publicURL, hint string
		disabled, missingAssets        bool
	}{
		{name: "native default", runtime: "native"},
		{name: "native override", runtime: "native", publicURL: "https://admin.example.test/commander/"},
		{name: "Docker default", runtime: "docker"},
		{name: "Docker Compose default", runtime: "docker-compose"},
		{name: "Docker custom mapping", runtime: "docker", publicURL: "http://localhost:14275"},
		{name: "disabled", runtime: "native", disabled: true, hint: "Admin is disabled."},
		{name: "unavailable", runtime: "native", missingAssets: true, publicURL: "https://admin.example.test", hint: "Admin UI is unavailable."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allInOneEnvironment(t)
			t.Setenv("API_RUNTIME", tc.runtime)
			t.Setenv("API_USERNAME", "operator+local")
			t.Setenv("COMMANDER_ADMIN_URL", tc.publicURL)
			args := []string{"--all-in-one", "--spawn-agents="}
			if tc.disabled {
				args = append(args, "--no-admin")
			}
			if tc.missingAssets {
				t.Setenv("UI_STATIC_DIR", filepath.Join(t.TempDir(), "missing"))
			}
			want := tc.hint
			if want == "" {
				want = "username=operator%2Blocal"
			}
			output := lookupAdmin(t, args, want)
			for _, secret := range []string{"all-in-one-api-secret", "all-in-one-mcp-secret"} {
				if strings.Contains(output, secret) {
					t.Fatal("Admin lookup exposed credentials")
				}
			}
			if tc.hint != "" {
				if strings.Contains(output, "#/connect") || strings.Contains(output, "Admin:http") {
					t.Fatal("unavailable Admin advertised a link")
				}
				return
			}
			match := regexp.MustCompile(`Admin:(https?://[^\s]+?username=operator%2Blocal)`).FindStringSubmatch(output)
			if len(match) != 2 {
				t.Fatal("Admin lookup did not contain its prefill link")
			}
			address, err := url.Parse(match[1])
			if err != nil || !strings.HasPrefix(address.Fragment, "/connect?") {
				t.Fatal("invalid Admin prefill URL")
			}
			params, err := url.ParseQuery(strings.TrimPrefix(address.EscapedFragment(), "/connect?"))
			if err != nil || len(params) != 2 || params.Get("username") != "operator+local" {
				t.Fatalf("Admin prefill did not preserve the session username: %v (%v)", params, err)
			}
			api, err := url.Parse(params.Get("api"))
			if err != nil || api.Hostname() != "127.0.0.1" || api.Port() == "" || api.User != nil {
				t.Fatal("Admin prefill did not use the actual session API")
			}
			address.Fragment, address.RawFragment = "", ""
			if tc.publicURL != "" && strings.TrimRight(address.String(), "/") != strings.TrimRight(tc.publicURL, "/") {
				t.Fatal("Admin public address or base path override was lost")
			}
			if tc.publicURL == "" {
				if tc.runtime == "native" {
					if address.Hostname() != "127.0.0.1" || address.Port() == "" {
						t.Fatal("native Admin did not use its ready address")
					}
				} else if address.String() != "http://localhost:4275/" {
					t.Fatal("Docker Admin did not use its default browser address")
				}
			}
		})
	}
}

// Exercise the real CLI during provider selection, without a model connection.
func lookupAdmin(t *testing.T, args []string, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	input, keyboard := io.Pipe()
	defer input.Close()
	defer keyboard.Close()
	var output terminalOutput
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- cli.Run(ctx, append(args, "--provider=", "--model=", "--base-url=", "--no-color"), input, &output, &output)
	}()
	t.Cleanup(func() {
		cancel()
		keyboard.Close()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("Admin lookup left Playground running")
		}
	})
	compact := func(text string) string { return strings.Join(strings.Fields(text), "") }
	wait := func(want string) {
		t.Helper()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for !strings.Contains(compact(output.String()), compact(want)) {
			select {
			case err := <-done:
				t.Fatalf("CLI stopped before Admin lookup: %v", err)
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %q", want)
			case <-tick.C:
			}
		}
	}
	wait("Select your model provider")
	if _, err := io.WriteString(keyboard, "/admin\r"); err != nil {
		t.Fatal(err)
	}
	wait(want)
	if _, err := io.WriteString(keyboard, "/exit\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Admin lookup did not exit")
	}
	return compact(output.String())
}
