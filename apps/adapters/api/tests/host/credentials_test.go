package api_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/cli"
	"github.com/defos-ai/commander/framework/host"
)

func TestMissingCredentialsExplainHowToStart(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_USERNAME", "")
	t.Setenv("API_PASSWORD", "")
	var output, diagnostic bytes.Buffer
	if code := cli.Main(fixture.localArgs(), &output, &diagnostic); code != 1 {
		t.Fatalf("missing credentials exit code = %d, want 1", code)
	}
	for _, instruction := range []string{"export API_USERNAME=commander", `export API_PASSWORD="$(openssl rand -hex 32)"`, "commander-api"} {
		if !strings.Contains(diagnostic.String(), instruction) {
			t.Errorf("missing credential diagnostic omits setup instruction %q", instruction)
		}
	}
	if output.Len() != 0 {
		t.Error("failed startup wrote to standard output")
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "database")); !os.IsNotExist(err) {
		t.Error("failed authentication setup opened the database")
	}
}

func TestCredentialsCommandReadsOnlyEnvironment(t *testing.T) {
	newFixture(t)
	t.Setenv("DATABASE_URL", "unsupported://do-not-open")
	t.Setenv("PORT", "invalid-do-not-parse")
	var output, diagnostic bytes.Buffer
	if code := cli.Main([]string{"credentials"}, &output, &diagnostic); code != 0 {
		t.Fatalf("credential retrieval failed with exit code %d", code)
	}
	if output.String() != "Username: api-fixture-user\nPassword: api-fixture-password\n" || diagnostic.Len() != 0 {
		t.Error("credential command did not print effective environment credentials only to standard output")
	}
}

func TestCredentialsCommandRaw(t *testing.T) {
	newFixture(t)
	t.Setenv("DATABASE_URL", "unsupported://do-not-open")
	t.Setenv("PORT", "invalid-do-not-parse")
	var output, diagnostic bytes.Buffer
	if code := cli.Main([]string{"credentials", "--raw"}, &output, &diagnostic); code != 0 {
		t.Fatalf("raw credential retrieval failed with exit code %d", code)
	}
	if output.String() != "api-fixture-password\n" || diagnostic.Len() != 0 {
		t.Error("raw credential command must print only the password and a newline")
	}
}

func TestEmbeddedCredentialsMustBeComplete(t *testing.T) {
	fixture := newFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := cli.RunWithOptions(ctx, fixture.localArgs(), io.Discard, cli.Options{
		Credentials: &host.BasicCredentials{Username: "embedded-user"},
	})
	if err == nil || !strings.Contains(err.Error(), "API_USERNAME and API_PASSWORD") {
		t.Fatal("incomplete embedded credentials must fail instead of using environment credentials")
	}
}

func TestCredentialsCommandRejectsInvalidCredentials(t *testing.T) {
	for _, test := range []struct{ name, username, password string }{
		{name: "missing"},
		{name: "username only", username: "private-command-user"},
		{name: "password only", password: "private-command-password"},
		{name: "invalid username", username: "private:command:user", password: "private-command-password"},
		{name: "invalid password", username: "private-command-user", password: "private\ncommand\npassword"},
	} {
		t.Run(test.name, func(t *testing.T) {
			newFixture(t)
			t.Setenv("API_USERNAME", test.username)
			t.Setenv("API_PASSWORD", test.password)
			for _, args := range [][]string{{"credentials"}, {"credentials", "--raw"}} {
				var output, diagnostic bytes.Buffer
				if code := cli.Main(args, &output, &diagnostic); code != 1 || output.Len() != 0 || diagnostic.Len() == 0 {
					t.Error("retrieving invalid credentials must fail with a diagnostic and no credentials")
				}
				for _, value := range []string{test.username, test.password} {
					if value != "" && strings.Contains(diagnostic.String(), value) {
						t.Error("credential retrieval failure leaked an environment credential")
					}
				}
			}
		})
	}
}

func TestCredentialsCommandHelpDoesNotRevealCredentials(t *testing.T) {
	newFixture(t)
	t.Setenv("API_USERNAME", "invalid:private-help-user")
	t.Setenv("API_PASSWORD", "private-help-password")
	var output, diagnostic bytes.Buffer
	if code := cli.Main([]string{"credentials", "--help"}, &output, &diagnostic); code != 0 {
		t.Fatalf("credential help failed with exit code %d", code)
	}
	if output.Len() != 0 || !strings.Contains(diagnostic.String(), "Usage: commander-api credentials [--raw]\n") {
		t.Error("credential help must describe the command without retrieving credentials")
	}
	for _, value := range []string{"invalid:private-help-user", "private-help-password", "--pid"} {
		if strings.Contains(diagnostic.String(), value) {
			t.Error("credential help includes a secret or an unsupported option")
		}
	}
}

func TestCredentialsCommandRejectsArgumentsWithoutEchoing(t *testing.T) {
	newFixture(t)
	const secret = "private-argument-password"
	for _, args := range [][]string{{"credentials", secret}, {"credentials", "--raw=" + secret}, {"credentials", "--" + secret}} {
		var output, diagnostic bytes.Buffer
		if code := cli.Main(args, &output, &diagnostic); code != 1 || output.Len() != 0 || diagnostic.Len() == 0 {
			t.Error("invalid credential arguments must fail without printing credentials")
		}
		if strings.Contains(diagnostic.String(), secret) || strings.Contains(diagnostic.String(), "api-fixture-password") {
			t.Error("invalid credential arguments leaked a value")
		}
	}
}
