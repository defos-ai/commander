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
	NetworkMode string   `yaml:"network_mode"`
	Entrypoint  []string
	Command     []string
	Environment map[string]string
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

type composeCredentialKind struct {
	name, variable, playgroundVariable, file, mount string
}

var composeCredentialKinds = []composeCredentialKind{
	{"mcp", "MCP_TOKEN", "COMMANDER_MCP_TOKEN", "mcp-token", "/run/commander"},
	{"api", "API_PASSWORD", "COMMANDER_API_PASSWORD", "api-password", "/run/commander-api"},
}

type composeCredentialFixture struct {
	dir, state string
	kind       composeCredentialKind
	services   map[string]credentialComposeService
}

func newComposeCredentialFixture(t *testing.T, kind composeCredentialKind) *composeCredentialFixture {
	t.Helper()
	f := &composeCredentialFixture{dir: t.TempDir(), kind: kind, services: credentialComposeServices(t)}
	f.state = filepath.Join(f.dir, kind.name+"-credentials")
	for _, directory := range []string{"mcp-credentials", "api-credentials", "secrets"} {
		if err := os.Mkdir(filepath.Join(f.dir, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.dir, "secrets/key"), []byte(strings.Repeat("ab", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, binary := range []string{"commander-mcp", "commander-api", "commander"} {
		mock := `#!/bin/sh
set -eu
if [ "${1:-}" = credentials ]; then
  test "$2" = --raw
  case "$0" in
    *commander-mcp)
      value="$MCP_TOKEN"
      case "$value" in ''|*[!a-zA-Z0-9._~+/-]*) exit 1 ;; esac
      ;;
    *commander-api)
      value="$API_PASSWORD"
      test -n "$(printf '%s' "$value" | tr -d '[:space:]')"
      case "$value" in *'
'*|*'	'*) exit 1 ;; esac
      ;;
  esac
  printf '%s\n' "$value"
else
  case "$0" in
    *commander-mcp) printf '%s' "$MCP_TOKEN" > "$FIXTURE_DIR/observed-mcp" ;;
    *commander-api) printf '%s' "$API_PASSWORD" > "$FIXTURE_DIR/observed-api" ;;
    *)
      printf '%s' "$COMMANDER_MCP_TOKEN" > "$FIXTURE_DIR/observed-mcp"
      printf '%s' "$COMMANDER_API_PASSWORD" > "$FIXTURE_DIR/observed-api"
      ;;
  esac
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

func (f *composeCredentialFixture) run(t *testing.T, service, credential string, arguments ...string) error {
	t.Helper()
	return f.runWithEnvironment(t, service, credential, nil, arguments...)
}

func (f *composeCredentialFixture) runWithEnvironment(t *testing.T, service, credential string, environment map[string]string, arguments ...string) error {
	t.Helper()
	spec := f.services[service]
	var script, name string
	if len(spec.Entrypoint) == 2 && slices.Equal(spec.Entrypoint, []string{"/bin/sh", "-ec"}) && len(spec.Command) == 1 {
		script = spec.Command[0]
		name = service
	} else if len(spec.Entrypoint) == 4 && slices.Equal(spec.Entrypoint[:2], []string{"/bin/sh", "-ec"}) {
		script = spec.Entrypoint[2]
		name = spec.Entrypoint[3]
	} else {
		t.Fatal("credential initialization and consumers must define one shell command")
	}
	script = strings.ReplaceAll(script, "$$", "$")
	if service == f.kind.name+"-credentials" {
		// The local fixture uses the runner's group instead of the container group.
		script = strings.ReplaceAll(script, "chgrp 1000", "chgrp "+strconv.Itoa(os.Getgid()))
	} else {
		script = "printf '%s' \"$$\" > \"$FIXTURE_DIR/before-pid\"\n" + script
	}
	script = strings.NewReplacer(
		"/run/commander-api", filepath.Join(f.dir, "api-credentials"),
		"/run/commander-secrets", filepath.Join(f.dir, "secrets"),
		"/run/commander", filepath.Join(f.dir, "mcp-credentials"),
	).Replace(script)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", append([]string{"-ec", script, name}, arguments...)...)
	values := map[string]string{
		"PATH": f.dir + ":/usr/bin:/bin", "FIXTURE_DIR": f.dir,
		"MCP_TOKEN": "other-token", "COMMANDER_MCP_TOKEN": "other-token",
		"API_USERNAME": "commander", "API_PASSWORD": "other-password",
		"COMMANDER_API_USERNAME": "commander", "COMMANDER_API_PASSWORD": "other-password",
		"COMMANDER_API_URL": "http://api:4274",
	}
	variable := f.kind.variable
	if service == "playground" {
		variable = f.kind.playgroundVariable
	}
	values[variable] = credential
	for key, value := range environment {
		values[key] = value
	}
	for key, value := range values {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.CombinedOutput()
	saved, _ := os.ReadFile(filepath.Join(f.state, f.kind.file))
	for _, secret := range []string{credential, strings.TrimSpace(string(saved))} {
		if secret != "" && strings.Contains(string(output), secret) {
			t.Fatal("credential startup exposed a secret in its output")
		}
	}
	return err
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
	for _, kind := range composeCredentialKinds {
		t.Run(kind.name, func(t *testing.T) {
			f := newComposeCredentialFixture(t, kind)
			service := kind.name + "-credentials"
			if err := f.run(t, service, ""); err != nil {
				t.Fatalf("credential generation failed: %v", err)
			}
			path := kind.name + "-credentials/" + kind.file
			credential := f.read(t, path)
			if len(strings.TrimSpace(credential)) < 32 {
				t.Fatal("generated credential is empty or too short")
			}
			for path, mode := range map[string]os.FileMode{kind.name + "-credentials": 0750, path: 0440} {
				info, err := os.Stat(filepath.Join(f.dir, path))
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("%s must be readable by the consumer group and inaccessible to others", path)
				}
			}
			if err := f.run(t, service, ""); err != nil {
				t.Fatalf("credential reuse failed: %v", err)
			}
			if f.read(t, path) != credential {
				t.Fatal("restart replaced the saved credential")
			}
			for _, override := range []string{"configured-credential-first", "configured-credential-second"} {
				if err := f.run(t, service, override); err != nil {
					t.Fatalf("explicit credential update failed: %v", err)
				}
				if strings.TrimSpace(f.read(t, path)) != override {
					t.Fatal("explicit credential was not saved for both consumers")
				}
			}
			before := f.read(t, path)
			if err := f.run(t, service, "invalid\ncredential"); err == nil {
				t.Fatal("credential validation did not reject an invalid override")
			}
			if f.read(t, path) != before {
				t.Fatal("invalid override replaced the saved credential")
			}
			entries, err := os.ReadDir(f.state)
			if err != nil || len(entries) != 1 || entries[0].Name() != kind.file {
				t.Fatal("credential initialization left temporary files")
			}
		})
	}
}

func TestCIComposeCredentialInitializationRejectsInvalidState(t *testing.T) {
	for _, credentialKind := range composeCredentialKinds {
		for _, state := range []string{"empty", "directory", "symlink", "dangling symlink", "invalid API password"} {
			if state == "invalid API password" && credentialKind.name != "api" {
				continue
			}
			t.Run(credentialKind.name+"/"+state, func(t *testing.T) {
				f := newComposeCredentialFixture(t, credentialKind)
				path := filepath.Join(f.state, credentialKind.file)
				var err error
				switch state {
				case "empty":
					err = os.WriteFile(path, nil, 0600)
				case "invalid API password":
					err = os.WriteFile(path, []byte("   \t"), 0600)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "symlink":
					if err = os.WriteFile(filepath.Join(f.dir, "outside"), []byte("existing test credential"), 0600); err == nil {
						err = os.Symlink(filepath.Join(f.dir, "outside"), path)
					}
				case "dangling symlink":
					err = os.Symlink(filepath.Join(f.dir, "absent"), path)
				}
				if err != nil {
					t.Fatal(err)
				}
				before, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(path)
				for _, override := range []string{"", "override-must-not-replace-invalid-state"} {
					if err := f.run(t, credentialKind.name+"-credentials", override); err == nil {
						t.Fatal("invalid credential state was accepted")
					}
					after, err := os.Lstat(path)
					if err != nil || !os.SameFile(before, after) {
						t.Fatal("initialization replaced invalid persisted state")
					}
					current, _ := os.ReadFile(path)
					if string(current) != string(data) {
						t.Fatal("initialization changed invalid persisted bytes")
					}
				}
			})
		}
	}
}

func TestCIComposeCredentialConsumers(t *testing.T) {
	for _, kind := range composeCredentialKinds {
		for _, service := range []string{kind.name, "playground"} {
			t.Run(kind.name+"/"+service, func(t *testing.T) {
				for _, override := range []string{"", "explicit-consumer-credential"} {
					f := newComposeCredentialFixture(t, kind)
					if err := f.run(t, kind.name+"-credentials", "shared-test-credential"); err != nil {
						t.Fatalf("credential init failed: %v", err)
					}
					arguments := []string{"--option=value with spaces", "", "--another"}
					if err := f.run(t, service, override, arguments...); err != nil {
						t.Fatalf("consumer startup failed: %v", err)
					}
					want := "shared-test-credential"
					if override != "" && service != "api" {
						want = override
					}
					if f.read(t, "observed-"+kind.name) != want {
						t.Fatal("consumer did not receive the selected credential")
					}
					if len(f.services[service].Entrypoint) == 4 && f.read(t, "observed-arguments") != strings.Join(arguments, "\n")+"\n" {
						t.Fatal("entrypoint changed the application arguments")
					}
					if f.read(t, "before-pid") != f.read(t, "after-pid") {
						t.Fatal("entrypoint did not replace the shell process")
					}
				}
				f := newComposeCredentialFixture(t, kind)
				if err := f.run(t, service, ""); err == nil {
					t.Fatal("consumer started without its credential file")
				}
				if _, err := os.Stat(filepath.Join(f.dir, "observed-"+kind.name)); !os.IsNotExist(err) {
					t.Fatal("application launched before reading the credential")
				}
				err := f.run(t, service, "explicit-without-file")
				if service == "api" {
					if err == nil {
						t.Fatal("API must always use the initializer's persisted password")
					}
				} else if err != nil || f.read(t, "observed-"+kind.name) != "explicit-without-file" {
					t.Fatal("consumer must retain explicit credentials without requiring a saved file")
				}
			})
		}
	}
	// Environment and argument overrides must never inherit the local stack's password.
	for _, target := range []struct {
		name, url string
		arguments []string
	}{
		{name: "other host", url: "http://other-api:4274"},
		{name: "trailing slash", url: "http://api:4274/"},
		{name: "other scheme", url: "https://api:4274"},
		{name: "long flag", url: "http://api:4274", arguments: []string{"--api-url", "http://other-api:4274"}},
		{name: "short flag", url: "http://api:4274", arguments: []string{"-api-url", "http://other-api:4274"}},
		{name: "long equals flag", url: "http://api:4274", arguments: []string{"--api-url=http://other-api:4274"}},
		{name: "short equals flag", url: "http://api:4274", arguments: []string{"-api-url=http://other-api:4274"}},
		{name: "explicit local flag", url: "http://api:4274", arguments: []string{"--api-url=http://api:4274"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			f := newComposeCredentialFixture(t, composeCredentialKinds[1])
			if err := f.run(t, "api-credentials", "shared-test-credential"); err != nil {
				t.Fatalf("credential init failed: %v", err)
			}
			for _, override := range []string{"", "custom-api-password"} {
				if err := f.runWithEnvironment(t, "playground", override, map[string]string{"COMMANDER_API_URL": target.url}, target.arguments...); err != nil {
					t.Fatalf("custom API startup failed: %v", err)
				}
				if f.read(t, "observed-api") != override {
					t.Fatal("custom API received the Compose password or lost its explicit password")
				}
				if f.read(t, "observed-arguments") != strings.Join(target.arguments, "\n")+"\n" {
					t.Fatal("credential guard changed the API arguments")
				}
			}
		})
	}
}

func TestCIComposeCredentialWiring(t *testing.T) {
	services := credentialComposeServices(t)
	for _, kind := range composeCredentialKinds {
		setup := services[kind.name+"-credentials"]
		if setup.Image != services[kind.name].Image || setup.User != "0:1000" || !setup.ReadOnly ||
			!slices.Contains(setup.CapDrop, "ALL") || !slices.Contains(setup.SecurityOpt, "no-new-privileges:true") {
			t.Fatalf("%s credential setup must reuse its adapter image with restricted root/group access", kind.name)
		}
		volume := kind.name + "-credentials:" + kind.mount
		if !slices.Equal(setup.Volumes, []string{volume}) {
			t.Fatalf("%s credential setup must own its writable credential volume", kind.name)
		}
		for _, name := range []string{kind.name, "playground"} {
			service := services[name]
			if !slices.Contains(service.Volumes, volume+":ro") ||
				service.DependsOn[kind.name+"-credentials"].Condition != "service_completed_successfully" ||
				!slices.Contains(service.CapDrop, "ALL") || !slices.Contains(service.SecurityOpt, "no-new-privileges:true") {
				t.Fatalf("%s must wait for %s setup and mount credentials read-only without extra privileges", name, kind.name)
			}
		}
	}
	apiSetup := services["api-credentials"]
	if apiSetup.NetworkMode != "none" || apiSetup.Environment["API_USERNAME"] != "${API_USERNAME:-commander}" ||
		apiSetup.Environment["API_PASSWORD"] != "${API_PASSWORD:-}" {
		t.Fatal("API credential initialization must validate optional configured credentials without network access")
	}
	if api := services["api"]; api.Init == nil || *api.Init {
		t.Fatal("API must remain PID 1")
	}
	if services["playground"].DependsOn["api"].Condition != "service_healthy" {
		t.Fatal("Playground must wait until its local API is ready")
	}
	for name, service := range services {
		if name == "api-credentials" || name == "api" || name == "playground" {
			continue
		}
		for _, volume := range service.Volumes {
			if strings.HasPrefix(volume, "api-credentials:") {
				t.Fatalf("%s must not receive the API credential volume", name)
			}
		}
	}
	var compose struct{ Volumes map[string]any }
	readCIYAML(t, "../..", "compose.yaml", &compose)
	if _, exists := compose.Volumes["api-credentials"]; !exists {
		t.Fatal("Compose must declare the persistent API credential volume")
	}
	mcp := services["mcp"]
	if mcp.Init == nil || *mcp.Init || !slices.Equal(mcp.Command, []string{"--listen-address=:4273", "--allowed-hosts=localhost,127.0.0.1,::1,mcp"}) {
		t.Fatal("MCP must remain PID 1 with its configured listen address and allowed hosts")
	}
}
