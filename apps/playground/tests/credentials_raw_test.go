package playground_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/playground/cli"
)

func TestCredentialCommandRaw(t *testing.T) {
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
			err := cli.Run(t.Context(), []string{"credentials", test.target, "--raw"}, credentialCommandInput{t}, &output, &diagnostic)
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
		if err := cli.Run(t.Context(), args, credentialCommandInput{t}, &output, &diagnostic); err != nil {
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
	for _, args := range [][]string{{"credentials", "api", "--raw", "private-argument"}, {"credentials", "mcp", "--raw=private-argument"}, {"credentials", "--raw", "api"}} {
		var output, diagnostic bytes.Buffer
		err := cli.Run(t.Context(), args, credentialCommandInput{t}, &output, &diagnostic)
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

type credentialCommandInput struct{ t *testing.T }

func (r credentialCommandInput) Read([]byte) (int, error) {
	r.t.Error("credential command attempted to read interactive input")
	return 0, io.EOF
}
