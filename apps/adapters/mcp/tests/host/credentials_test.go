package mcp_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/host"
)

func TestMissingCredentialsExplainHowToStart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MCP_TOKEN", "")
	var output, diagnostic bytes.Buffer
	if code := cli.Main(hostfixtures.Args(root), &output, &diagnostic); code != 1 {
		t.Fatalf("missing credentials exit code = %d, want 1", code)
	}
	for _, instruction := range []string{"MCP_TOKEN", `export MCP_TOKEN="$(openssl rand -hex 32)"`, "$env:MCP_TOKEN", "commander-mcp"} {
		if !strings.Contains(diagnostic.String(), instruction) {
			t.Errorf("missing credential diagnostic omits setup instruction %q", instruction)
		}
	}
	if output.Len() != 0 {
		t.Error("failed startup wrote to standard output")
	}
	if _, err := os.Stat(filepath.Join(root, "database")); !os.IsNotExist(err) {
		t.Error("failed authentication setup opened the database")
	}
}

func TestEmbeddedCredentialsMustBeComplete(t *testing.T) {
	t.Setenv("MCP_TOKEN", "environment-token")
	err := cli.RunWithOptions(t.Context(), hostfixtures.Args(t.TempDir()), io.Discard, cli.Options{Credentials: &host.BearerCredentials{}})
	if err == nil || !strings.Contains(err.Error(), "MCP_TOKEN must be set") {
		t.Fatal("empty embedded credentials must fail instead of using environment credentials")
	}
}

func TestCredentialsCommand(t *testing.T) {
	const secret = "mcp-lookup-token"
	t.Setenv("MCP_TOKEN", secret)
	t.Setenv("DATABASE_URL", "unsupported://do-not-open")
	var output, diagnostic bytes.Buffer
	if code := cli.Main([]string{"credentials"}, &output, &diagnostic); code != 0 {
		t.Fatalf("credential retrieval failed with exit code %d", code)
	}
	if output.String() != "Token: "+secret+"\n" || diagnostic.Len() != 0 {
		t.Error("credential command did not print effective environment credentials only to standard output")
	}
	output.Reset()
	diagnostic.Reset()
	if code := cli.Main([]string{"credentials", "--raw"}, &output, &diagnostic); code != 0 {
		t.Fatalf("raw credential retrieval failed with exit code %d", code)
	}
	if output.String() != secret+"\n" || diagnostic.Len() != 0 {
		t.Error("raw credential command must print only the token and a newline")
	}
	t.Setenv("MCP_TOKEN", "private invalid-help-token")
	for _, args := range [][]string{{"--help"}, {"credentials", "--help"}} {
		output.Reset()
		diagnostic.Reset()
		if code := cli.Main(args, &output, &diagnostic); code != 0 {
			t.Fatalf("help failed with exit code %d", code)
		}
		if output.Len() != 0 || !strings.Contains(diagnostic.String(), "commander-mcp") {
			t.Error("help must describe the command without retrieving credentials")
		}
		for _, value := range []string{secret, "private invalid-help-token", "--pid"} {
			if strings.Contains(diagnostic.String(), value) {
				t.Error("help includes a secret or an unsupported option")
			}
		}
	}
}

func TestCLIRejectsInvalidCredentialsWithoutLeaking(t *testing.T) {
	for _, secret := range []string{"", " ", "private invalid token", "private\ninvalid-token", "private=invalid-token"} {
		t.Setenv("MCP_TOKEN", secret)
		for _, args := range [][]string{hostfixtures.Args(t.TempDir()), {"credentials"}, {"credentials", "--raw"}} {
			var output, diagnostic bytes.Buffer
			if code := cli.Main(args, &output, &diagnostic); code != 1 || output.Len() != 0 || diagnostic.Len() == 0 {
				t.Error("invalid credentials must fail with a diagnostic and no credentials")
			}
			if strings.TrimSpace(secret) != "" && strings.Contains(diagnostic.String(), secret) {
				t.Error("failure leaked an environment credential")
			}
		}
	}
}

func TestCredentialsCommandRejectsArgumentsWithoutEchoing(t *testing.T) {
	t.Setenv("MCP_TOKEN", "private-environment-token")
	const secret = "private-argument-token"
	for _, args := range [][]string{{"credentials", secret}, {"credentials", "--raw=" + secret}, {"credentials", "--" + secret}} {
		var output, diagnostic bytes.Buffer
		if code := cli.Main(args, &output, &diagnostic); code != 1 || output.Len() != 0 || diagnostic.Len() == 0 {
			t.Error("invalid credential arguments must fail without printing credentials")
		}
		if strings.Contains(diagnostic.String(), secret) || strings.Contains(diagnostic.String(), "private-environment-token") {
			t.Error("invalid credential arguments leaked a value")
		}
	}
}
