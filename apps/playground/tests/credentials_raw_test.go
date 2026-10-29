package playground_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	fakes "github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestCredentialCommandRaw(t *testing.T) {
	t.Setenv("COMMANDER_STATE_DIR", fixtures.LocalStateDirectory(t))
	t.Setenv("COMMANDER_MCP_URL", "invalid-url")
	t.Setenv("COMMANDER_DEBUG", "invalid-boolean")
	for _, test := range []struct {
		name, target, username, password, token, want string
	}{
		{name: "API", target: "api", username: "commander", password: "fixture-password", token: "invalid token", want: "fixture-password\n"},
		{name: "MCP", target: "mcp", username: "invalid:username", password: "fixture-password", token: "fixture-token", want: "fixture-token\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("API_USERNAME", test.username)
			t.Setenv("API_PASSWORD", test.password)
			t.Setenv("MCP_TOKEN", test.token)
			var output, diagnostic bytes.Buffer
			err := cli.Run(t.Context(), []string{"credentials", test.target, "--raw"}, fakes.NewCredentialCommandInput(t), &output, &diagnostic)
			if diagnostic.Len() != 0 {
				t.Fatal("credential command wrote startup output or diagnostics")
			}
			if err != nil || output.String() != test.want {
				t.Fatal("raw credential command did not print only the selected secret and a newline")
			}
		})
	}
}

func TestCredentialCommandHelpWithoutLookup(t *testing.T) {
	t.Setenv("API_USERNAME", "private:help-user")
	t.Setenv("API_PASSWORD", "private\nhelp-password")
	t.Setenv("MCP_TOKEN", "private invalid-help-token")
	t.Setenv("COMMANDER_MCP_URL", "invalid-url")
	for _, args := range [][]string{{"credentials", "--help"}, {"credentials", "api", "--help"}, {"credentials", "mcp", "--raw", "--help"}} {
		var output, diagnostic bytes.Buffer
		if err := cli.Run(t.Context(), args, fakes.NewCredentialCommandInput(t), &output, &diagnostic); err != nil {
			t.Fatalf("credential help failed: %v", err)
		}
		if !strings.Contains(output.String(), "Usage: commander credentials api|mcp [--raw]") || diagnostic.Len() != 0 {
			t.Fatal("credential help did not describe the command without startup diagnostics")
		}
		for _, value := range []string{"private:help-user", "private\nhelp-password", "private invalid-help-token"} {
			if strings.Contains(output.String(), value) {
				t.Fatal("credential help leaked a secret")
			}
		}
	}
}

func TestCredentialCommandRawRejectsArguments(t *testing.T) {
	t.Setenv("API_USERNAME", "commander")
	t.Setenv("API_PASSWORD", "private-environment-password")
	t.Setenv("MCP_TOKEN", "private-environment-token")
	for _, args := range [][]string{{"credentials", "api", "--raw", "private-argument"}, {"credentials", "mcp", "--raw=private-argument"}, {"credentials", "--raw", "api"}, {"credentials", "api", "--state-dir"}, {"credentials", "api", "--state-dir="}, {"credentials", "api", "--state-dir", "--raw"}} {
		var output, diagnostic bytes.Buffer
		err := cli.Run(t.Context(), args, fakes.NewCredentialCommandInput(t), &output, &diagnostic)
		if err == nil || output.Len() != 0 || diagnostic.Len() != 0 {
			t.Fatal("invalid raw command must fail without printing credentials or starting Playground")
		}
		for _, value := range []string{"private-argument", "private-environment-password", "private-environment-token"} {
			if strings.Contains(err.Error(), value) {
				t.Fatal("invalid credential arguments echoed a private value")
			}
		}
	}
}

func TestCredentialCommandSavedState(t *testing.T) {
	fixtures.AllInOneEnvironment(t)
	directory := fixtures.LocalStateDirectory(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := local.StartWithOptions(ctx, local.Options{Directory: filepath.Join(directory, "local")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("prepare retained local state: %v", err)
	}
	for _, name := range []string{"API_USERNAME", "API_PASSWORD", "MCP_TOKEN"} {
		t.Setenv(name, "")
	}
	t.Setenv("COMMANDER_STATE_DIR", directory)
	for _, scenario := range []struct {
		name, want, username string
		args                 []string
	}{
		{name: "API from environment path", args: []string{"credentials", "api", "--raw"}, want: "all-in-one-api-secret\n"},
		{name: "MCP from explicit path", args: []string{"credentials", "mcp", "--state-dir", directory}, want: "Token: all-in-one-mcp-secret\n"},
		{name: "API explicit path assignment", args: []string{"credentials", "api", "--state-dir=" + directory}, want: "Username: commander\nPassword: all-in-one-api-secret\n"},
		{name: "retained username", username: "commander", args: []string{"credentials", "api", "--state-dir=" + directory, "--raw"}, want: "all-in-one-api-secret\n"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("API_USERNAME", scenario.username)
			if scenario.name != "API from environment path" {
				t.Setenv("COMMANDER_STATE_DIR", fixtures.LocalStateDirectory(t))
			}
			var output, diagnostic bytes.Buffer

			err := cli.Run(t.Context(), scenario.args, fakes.NewCredentialCommandInput(t), &output, &diagnostic)

			if err != nil || output.String() != scenario.want || diagnostic.Len() != 0 {
				t.Fatal("saved credential lookup failed or started Playground")
			}
		})
	}
	t.Setenv("API_USERNAME", "explicit-user")
	t.Setenv("API_PASSWORD", "explicit-password")
	var output, diagnostic bytes.Buffer

	err := cli.Run(t.Context(), []string{"credentials", "api", "--raw"}, fakes.NewCredentialCommandInput(t), &output, &diagnostic)

	if err != nil || output.String() != "explicit-password\n" || diagnostic.Len() != 0 {
		t.Fatal("saved state replaced explicit environment credentials")
	}
}
