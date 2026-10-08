package api_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

func TestEmbeddedCredentialsMustBeComplete(t *testing.T) {
	fixture := newFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := cli.RunWithOptions(ctx, fixture.localArgs(), io.Discard, cli.Options{
		Credentials: &host.Credentials{Username: "embedded-user"},
	})
	if err == nil || !strings.Contains(err.Error(), "API_USERNAME and API_PASSWORD") {
		t.Fatal("incomplete embedded credentials must fail instead of using environment credentials")
	}
}

func TestCredentialsCommandRejectsMissingOrPartialCredentials(t *testing.T) {
	for _, test := range []struct{ name, username, password string }{
		{name: "missing"},
		{name: "username only", username: "private-command-user"},
		{name: "password only", password: "private-command-password"},
	} {
		t.Run(test.name, func(t *testing.T) {
			newFixture(t)
			t.Setenv("API_USERNAME", test.username)
			t.Setenv("API_PASSWORD", test.password)
			var output, diagnostic bytes.Buffer
			if code := cli.Main([]string{"credentials"}, &output, &diagnostic); code != 1 || output.Len() != 0 || diagnostic.Len() == 0 {
				t.Error("retrieving incomplete credentials must fail with a diagnostic and no credentials")
			}
			for _, value := range []string{test.username, test.password} {
				if value != "" && strings.Contains(diagnostic.String(), value) {
					t.Error("credential retrieval failure leaked an environment credential")
				}
			}
		})
	}
}

func TestCredentialsCommandReadsProcessEnvironment(t *testing.T) {
	if os.Getenv("COMMANDER_TEST_CREDENTIAL_PROCESS") == "1" {
		t.Setenv("API_USERNAME", "runtime-only-user")
		t.Setenv("API_PASSWORD", "runtime-only-password")
		os.Exit(cli.Main([]string{"credentials", "--pid", strconv.Itoa(os.Getpid())}, os.Stdout, os.Stderr))
	}
	if runtime.GOOS != "linux" {
		t.Skip("process environment retrieval is Linux-specific")
	}
	for _, test := range []struct {
		name     string
		password string
	}{
		{name: "configured", password: "private-process-password"},
		{name: "missing password"},
	} {
		t.Run(test.name, func(t *testing.T) {
			newFixture(t)
			t.Setenv("API_USERNAME", "private-process-user")
			t.Setenv("API_PASSWORD", test.password)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCredentialsCommandReadsProcessEnvironment$")
			command.Env = append(os.Environ(), "COMMANDER_TEST_CREDENTIAL_PROCESS=1")
			var output, diagnostic bytes.Buffer
			command.Stdout, command.Stderr = &output, &diagnostic
			err := command.Run()
			if ctx.Err() != nil {
				t.Fatal("process credential retrieval timed out")
			}
			if test.password != "" {
				if err != nil || output.String() != "Username: private-process-user\nPassword: private-process-password\n" || diagnostic.Len() != 0 {
					t.Error("process retrieval did not return its startup environment credentials only to standard output")
				}
			} else if err == nil || output.Len() != 0 || diagnostic.Len() == 0 {
				t.Error("missing process credentials must fail without falling back to current environment")
			}
			for _, value := range []string{"private-process-user", "private-process-password", "runtime-only-user", "runtime-only-password"} {
				if strings.Contains(diagnostic.String(), value) {
					t.Error("process credential retrieval exposed credentials in diagnostics")
				}
			}
		})
	}
}

func TestCredentialsCommandRejectsInvalidProcessIDs(t *testing.T) {
	newFixture(t)
	for _, pid := range []string{"0", "-1", "not-a-pid", "2147483647"} {
		var output, diagnostic bytes.Buffer
		if code := cli.Main([]string{"credentials", "--pid", pid}, &output, &diagnostic); code != 1 || output.Len() != 0 || diagnostic.Len() == 0 {
			t.Errorf("invalid or absent process %q did not reject retrieval", pid)
		}
	}
}
