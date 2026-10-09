package release_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type credentialComposeService struct {
	Image       string
	User        string
	Init        *bool
	ReadOnly    bool     `yaml:"read_only"`
	CapDrop     []string `yaml:"cap_drop"`
	SecurityOpt []string `yaml:"security_opt"`
	Entrypoint  []string
	Command     []string
	Volumes     []string
	DependsOn   map[string]struct{ Condition string } `yaml:"depends_on"`
}

func credentialComposeServices(t *testing.T) map[string]credentialComposeService {
	t.Helper()
	var compose struct {
		Services map[string]credentialComposeService
	}
	readCIYAML(t, "../..", "compose.yaml", &compose)
	return compose.Services
}

type composeCredentialFixture struct {
	dir, state string
	services   map[string]credentialComposeService
}

func newComposeCredentialFixture(t *testing.T) *composeCredentialFixture {
	t.Helper()
	f := &composeCredentialFixture{dir: t.TempDir(), services: credentialComposeServices(t)}
	f.state = filepath.Join(f.dir, "credentials")
	if err := os.Mkdir(f.state, 0700); err != nil {
		t.Fatal(err)
	}
	for _, binary := range []string{"commander-mcp", "commander"} {
		variable := "MCP_TOKEN"
		if binary == "commander" {
			variable = "COMMANDER_MCP_TOKEN"
		}
		mock := `#!/bin/sh
set -eu
if [ "${1:-}" = credentials ]; then
  test "$2" = --raw
  printf '%s\n' "$MCP_TOKEN"
else
  printf '%s' "$` + variable + `" > "$FIXTURE_DIR/observed-token"
  printf '%s\n' "$@" > "$FIXTURE_DIR/observed-arguments"
  printf '%s' "$$" > "$FIXTURE_DIR/after-pid"
fi
`
		if err := os.WriteFile(filepath.Join(f.dir, binary), []byte(mock), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *composeCredentialFixture) run(t *testing.T, service, token string, arguments ...string) error {
	t.Helper()
	spec := f.services[service]
	script := ""
	name := "credentials"
	if service == "mcp-credentials" {
		if len(spec.Command) != 1 {
			t.Fatal("credential init must define one shell command")
		}
		script = spec.Command[0]
		// The local fixture uses the runner's group instead of the container group.
		script = strings.ReplaceAll(script, "chgrp 1000", "chgrp "+strconv.Itoa(os.Getgid()))
	} else {
		if len(spec.Entrypoint) != 4 {
			t.Fatal("credential consumers must preserve the shell command arguments")
		}
		script = spec.Entrypoint[2]
		name = spec.Entrypoint[3]
		script = "printf '%s' \"$$\" > \"$FIXTURE_DIR/before-pid\"\n" + strings.ReplaceAll(script, "$$", "$")
	}
	if service == "mcp-credentials" {
		script = strings.ReplaceAll(script, "$$", "$")
	}
	script = strings.ReplaceAll(script, "/run/commander", f.state)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", append([]string{"-ec", script, name}, arguments...)...)
	variable := "MCP_TOKEN"
	if service == "playground" {
		variable = "COMMANDER_MCP_TOKEN"
	}
	command.Env = []string{"PATH=" + f.dir + ":/usr/bin:/bin", "FIXTURE_DIR=" + f.dir, variable + "=" + token}
	return command.Run()
}

func (f *composeCredentialFixture) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCIComposeCredentialInitialization(t *testing.T) {
	f := newComposeCredentialFixture(t)
	if err := f.run(t, "mcp-credentials", ""); err != nil {
		t.Fatalf("credential generation failed: %v", err)
	}
	token := f.read(t, "credentials/mcp-token")
	if len(strings.TrimSpace(token)) < 32 {
		t.Fatal("generated credential is empty or too short")
	}
	for path, mode := range map[string]os.FileMode{"credentials": 0750, "credentials/mcp-token": 0440} {
		info, err := os.Stat(filepath.Join(f.dir, path))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s must be readable by the consumer group and inaccessible to others", path)
		}
	}
	if err := f.run(t, "mcp-credentials", ""); err != nil {
		t.Fatalf("credential reuse failed: %v", err)
	}
	if f.read(t, "credentials/mcp-token") != token {
		t.Fatal("restart replaced the saved credential")
	}
	for _, override := range []string{"configured-token-first", "configured-token-second"} {
		if err := f.run(t, "mcp-credentials", override); err != nil {
			t.Fatalf("explicit credential update failed: %v", err)
		}
		if strings.TrimSpace(f.read(t, "credentials/mcp-token")) != override {
			t.Fatal("explicit credential was not saved for both consumers")
		}
	}
	entries, err := os.ReadDir(f.state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "mcp-token" {
		t.Fatal("credential initialization left temporary files")
	}
}

func TestCIComposeCredentialInitializationRejectsInvalidState(t *testing.T) {
	for _, kind := range []string{"empty", "directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			f := newComposeCredentialFixture(t)
			path := filepath.Join(f.state, "mcp-token")
			var err error
			switch kind {
			case "empty":
				err = os.WriteFile(path, nil, 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				if err = os.WriteFile(filepath.Join(f.dir, "outside"), []byte("existing test token"), 0600); err == nil {
					err = os.Symlink(filepath.Join(f.dir, "outside"), path)
				}
			case "dangling symlink":
				err = os.Symlink(filepath.Join(f.dir, "absent"), path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.run(t, "mcp-credentials", "override-must-not-replace-invalid-state"); err == nil {
				t.Fatal("invalid credential state was accepted")
			}
		})
	}
}

func TestCIComposeCredentialConsumers(t *testing.T) {
	for _, service := range []string{"mcp", "playground"} {
		t.Run(service, func(t *testing.T) {
			for _, override := range []string{"", "explicit-consumer-token"} {
				f := newComposeCredentialFixture(t)
				if err := f.run(t, "mcp-credentials", "shared-test-token"); err != nil {
					t.Fatalf("credential init failed: %v", err)
				}
				arguments := []string{"--option=value with spaces", "", "--another"}
				if err := f.run(t, service, override, arguments...); err != nil {
					t.Fatalf("consumer startup failed: %v", err)
				}
				want := "shared-test-token"
				if override != "" {
					want = override
				}
				if f.read(t, "observed-token") != want {
					t.Fatal("consumer did not receive the selected credential")
				}
				if f.read(t, "observed-arguments") != strings.Join(arguments, "\n")+"\n" {
					t.Fatal("entrypoint changed the application arguments")
				}
				if f.read(t, "before-pid") != f.read(t, "after-pid") {
					t.Fatal("entrypoint did not replace the shell process")
				}
			}
			f := newComposeCredentialFixture(t)
			if err := f.run(t, service, ""); err == nil {
				t.Fatal("consumer started without its credential file")
			}
			if _, err := os.Stat(filepath.Join(f.dir, "observed-token")); !os.IsNotExist(err) {
				t.Fatal("application launched before reading the credential")
			}
			if err := f.run(t, service, "explicit-without-file"); err != nil {
				t.Fatalf("explicit credential should not require a saved file: %v", err)
			}
			if f.read(t, "observed-token") != "explicit-without-file" {
				t.Fatal("consumer ignored an explicit credential when no saved file existed")
			}
		})
	}
}

func TestCIComposeCredentialWiring(t *testing.T) {
	services := credentialComposeServices(t)
	setup := services["mcp-credentials"]
	if setup.Image != services["mcp"].Image || setup.User != "0:1000" || !setup.ReadOnly ||
		!slices.Contains(setup.CapDrop, "ALL") || !slices.Contains(setup.SecurityOpt, "no-new-privileges:true") {
		t.Fatal("credential setup must reuse the MCP image with restricted root/group access")
	}
	if !slices.Contains(setup.Volumes, "mcp-credentials:/run/commander") {
		t.Fatal("credential setup lacks its writable credential volume")
	}
	for _, name := range []string{"mcp", "playground"} {
		service := services[name]
		if !slices.Contains(service.Volumes, "mcp-credentials:/run/commander:ro") ||
			service.DependsOn["mcp-credentials"].Condition != "service_completed_successfully" ||
			!slices.Contains(service.CapDrop, "ALL") || !slices.Contains(service.SecurityOpt, "no-new-privileges:true") {
			t.Fatalf("%s must wait for setup and mount credentials read-only without extra privileges", name)
		}
	}
	mcp := services["mcp"]
	if mcp.Init == nil || *mcp.Init || !slices.Equal(mcp.Command, []string{"--listen-address=:4273", "--allowed-hosts=localhost,127.0.0.1,::1,mcp"}) {
		t.Fatal("MCP must remain PID 1 with its configured listen address and allowed hosts")
	}
}
