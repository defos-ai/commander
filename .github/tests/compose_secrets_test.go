package release_test

import (
	"context"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type composeSecretsFixture struct {
	dir, state string
	services   map[string]credentialComposeService
}

func newComposeSecretsFixture(t *testing.T) *composeSecretsFixture {
	t.Helper()
	f := &composeSecretsFixture{dir: t.TempDir(), services: credentialComposeServices(t)}
	f.state = filepath.Join(f.dir, "secrets")
	if err := os.Mkdir(f.state, 0700); err != nil {
		t.Fatal(err)
	}
	mock := `#!/bin/sh
set -eu
printf '%s' "$SECRETS_ENCRYPTION_KEY" > "$FIXTURE_DIR/observed-key"
printf '%s' "$API_PASSWORD" > "$FIXTURE_DIR/observed-password"
printf '%s\n' "$@" > "$FIXTURE_DIR/observed-arguments"
printf '%s' "$$" > "$FIXTURE_DIR/after-pid"
`
	for _, binary := range []string{"commander-api", "commander-builder-agent"} {
		if err := os.WriteFile(filepath.Join(f.dir, binary), []byte(mock), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *composeSecretsFixture) run(t *testing.T, service, key string, arguments ...string) error {
	t.Helper()
	spec := f.services[service]
	var script, name string
	if service == "builder-agent" {
		if len(spec.Entrypoint) != 4 || !slices.Equal(spec.Entrypoint[:2], []string{"/bin/sh", "-ec"}) {
			t.Fatal("builder startup must preserve the shell command arguments")
		}
		script, name = spec.Entrypoint[2], spec.Entrypoint[3]
	} else {
		if !slices.Equal(spec.Entrypoint, []string{"/bin/sh", "-ec"}) || len(spec.Command) != 1 {
			t.Fatal("secrets initialization and API startup must define one shell command")
		}
		script, name = spec.Command[0], service
	}
	script = strings.ReplaceAll(script, "$$", "$")
	script = strings.ReplaceAll(script, "/run/commander-secrets", f.state)
	// The local fixture uses the runner's group instead of the container group.
	script = strings.ReplaceAll(script, "chgrp 1000", "chgrp "+strconv.Itoa(os.Getgid()))
	if service != "secrets-key" {
		script = "printf '%s' \"$$\" > \"$FIXTURE_DIR/before-pid\"\n" + script
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", append([]string{"-ec", script, name}, arguments...)...)
	command.Env = []string{
		"PATH=" + f.dir + ":/usr/bin:/bin", "FIXTURE_DIR=" + f.dir,
		"SECRETS_ENCRYPTION_KEY=" + key, "API_PASSWORD=configured-api-password",
	}
	output, err := command.CombinedOutput()
	saved, _ := os.ReadFile(filepath.Join(f.state, "key"))
	for _, secret := range []string{key, strings.TrimSpace(string(saved)), "configured-api-password"} {
		if secret != "" && strings.Contains(strings.ToLower(string(output)), strings.ToLower(secret)) {
			t.Fatal("startup exposed a secret in its output")
		}
	}
	return err
}

func (f *composeSecretsFixture) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCIComposeSecretsInitialization(t *testing.T) {
	f := newComposeSecretsFixture(t)
	if err := f.run(t, "secrets-key", ""); err != nil {
		t.Fatalf("key generation failed: %v", err)
	}
	saved := f.read(t, "secrets/key")
	key := strings.TrimSpace(saved)
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		t.Fatal("generated key must encode 32 bytes as hexadecimal")
	}
	for path, mode := range map[string]os.FileMode{"secrets": 0750, "secrets/key": 0440} {
		info, err := os.Stat(filepath.Join(f.dir, path))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s must be readable by the consumer group and inaccessible to others", path)
		}
	}
	for _, override := range []string{"", key, strings.ToUpper(key)} {
		if err := f.run(t, "secrets-key", override); err != nil {
			t.Fatalf("saved key reuse failed: %v", err)
		}
		if f.read(t, "secrets/key") != saved {
			t.Fatal("restart changed the persisted key bytes")
		}
	}
	entries, err := os.ReadDir(f.state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "key" {
		t.Fatal("key initialization left temporary files")
	}
}

func TestCIComposeSecretsExplicitKey(t *testing.T) {
	f := newComposeSecretsFixture(t)
	configured := strings.Repeat("aB", 32)
	if err := f.run(t, "secrets-key", configured); err != nil {
		t.Fatalf("explicit key initialization failed: %v", err)
	}
	saved := f.read(t, "secrets/key")
	if !strings.EqualFold(strings.TrimSpace(saved), configured) {
		t.Fatal("explicit key was not persisted")
	}
	if err := f.run(t, "secrets-key", strings.ToLower(configured)); err != nil {
		t.Fatalf("equivalent hexadecimal override failed: %v", err)
	}
	for _, override := range []string{strings.Repeat("cd", 32), "invalid-key"} {
		if err := f.run(t, "secrets-key", override); err == nil {
			t.Fatal("conflicting or invalid override was accepted")
		}
		if f.read(t, "secrets/key") != saved {
			t.Fatal("override changed the persisted key bytes")
		}
	}
}

func TestCIComposeSecretsRejectsInvalidOverride(t *testing.T) {
	for _, override := range []string{strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("g", 64), strings.Repeat("a", 64) + "\n"} {
		f := newComposeSecretsFixture(t)
		if err := f.run(t, "secrets-key", override); err == nil {
			t.Fatal("override must contain exactly 64 hexadecimal characters")
		}
		if _, err := os.Stat(filepath.Join(f.state, "key")); !os.IsNotExist(err) {
			t.Fatal("invalid override created a persisted key")
		}
	}
}

func TestCIComposeSecretsRejectsInvalidState(t *testing.T) {
	for _, kind := range []string{"empty", "short", "nonhex", "directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			f := newComposeSecretsFixture(t)
			path := filepath.Join(f.state, "key")
			var err error
			switch kind {
			case "empty":
				err = os.WriteFile(path, nil, 0600)
			case "short":
				err = os.WriteFile(path, []byte("abcd"), 0600)
			case "nonhex":
				err = os.WriteFile(path, []byte(strings.Repeat("g", 64)), 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				if err = os.WriteFile(filepath.Join(f.dir, "outside"), []byte(strings.Repeat("ab", 32)), 0600); err == nil {
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
			for _, override := range []string{"", strings.Repeat("cd", 32)} {
				if err := f.run(t, "secrets-key", override); err == nil {
					t.Fatal("invalid persisted state was accepted")
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

func TestCIComposeSecretsConsumers(t *testing.T) {
	for _, service := range []string{"api", "builder-agent"} {
		t.Run(service, func(t *testing.T) {
			f := newComposeSecretsFixture(t)
			if err := f.run(t, "secrets-key", strings.Repeat("ab", 32)); err != nil {
				t.Fatalf("key initialization failed: %v", err)
			}
			var arguments []string
			if service == "builder-agent" {
				arguments = []string{"--workspace-dir=value with spaces", "", "--another"}
			}
			if err := f.run(t, service, strings.Repeat("cd", 32), arguments...); err != nil {
				t.Fatalf("consumer startup failed: %v", err)
			}
			if f.read(t, "observed-key") != strings.TrimSpace(f.read(t, "secrets/key")) {
				t.Fatal("consumer must load the persisted key even when its environment contains an override")
			}
			if f.read(t, "observed-password") != "configured-api-password" {
				t.Fatal("consumer startup changed its configured password")
			}
			if f.read(t, "observed-arguments") != strings.Join(arguments, "\n")+"\n" {
				t.Fatal("consumer startup changed the application arguments")
			}
			if f.read(t, "before-pid") != f.read(t, "after-pid") {
				t.Fatal("consumer startup did not replace the shell process")
			}
			for _, state := range []string{"missing", "empty"} {
				t.Run(state, func(t *testing.T) {
					f := newComposeSecretsFixture(t)
					if state == "empty" {
						if err := os.WriteFile(filepath.Join(f.state, "key"), nil, 0600); err != nil {
							t.Fatal(err)
						}
					}
					if err := f.run(t, service, strings.Repeat("ab", 32)); err == nil {
						t.Fatal("consumer started without a nonempty persisted key")
					}
					if _, err := os.Stat(filepath.Join(f.dir, "observed-key")); !os.IsNotExist(err) {
						t.Fatal("consumer launched before loading its persisted key")
					}
				})
			}
		})
	}
}

func TestCIComposeSecretsWiring(t *testing.T) {
	services := credentialComposeServices(t)
	setup := services["secrets-key"]
	if setup.Image != services["api"].Image || setup.User != "0:1000" || !setup.ReadOnly ||
		setup.NetworkMode != "none" || !slices.Contains(setup.CapDrop, "ALL") ||
		!slices.Contains(setup.SecurityOpt, "no-new-privileges:true") {
		t.Fatal("key initialization must reuse the API image with restricted root/group access and no network")
	}
	if !slices.Equal(setup.Volumes, []string{"secrets-key:/run/commander-secrets"}) ||
		setup.Environment["SECRETS_ENCRYPTION_KEY"] != "${SECRETS_ENCRYPTION_KEY:-}" {
		t.Fatal("key initialization must own its writable volume and accept the optional explicit key")
	}
	api := services["api"]
	if api.Init == nil || *api.Init {
		t.Fatal("API must remain PID 1")
	}
	for _, name := range []string{"api", "builder-agent"} {
		service := services[name]
		if !slices.Contains(service.Volumes, "secrets-key:/run/commander-secrets:ro") ||
			service.DependsOn["secrets-key"].Condition != "service_completed_successfully" {
			t.Fatalf("%s must wait for key initialization and mount its key read-only", name)
		}
	}
	builder := services["builder-agent"]
	if builder.Init == nil || !*builder.Init ||
		!slices.Contains(builder.Volumes, "builder-workspaces:/home/commander/workspaces") ||
		builder.DependsOn["controller"].Condition != "service_started" ||
		builder.DependsOn["rabbitmq"].Condition != "service_healthy" ||
		services["playground"].DependsOn["builder-agent"].Condition != "service_started" {
		t.Fatal("builder must retain init, persistent workspaces and agent dependencies, and start before Playground")
	}
	for name, service := range services {
		if name == "secrets-key" {
			continue
		}
		if _, exists := service.Environment["SECRETS_ENCRYPTION_KEY"]; exists {
			t.Fatalf("%s must not receive the key through Compose environment configuration", name)
		}
		if name == "api" || name == "builder-agent" {
			continue
		}
		for _, volume := range service.Volumes {
			if strings.HasPrefix(volume, "secrets-key:") {
				t.Fatalf("%s must not mount the secrets encryption key", name)
			}
		}
	}
	var compose struct{ Volumes map[string]any }
	readCIYAML(t, "../..", "compose.yaml", &compose)
	if _, exists := compose.Volumes["secrets-key"]; !exists {
		t.Fatal("Compose must declare a persistent secrets-key volume")
	}
	if _, exists := compose.Volumes["builder-workspaces"]; !exists {
		t.Fatal("Compose must declare a separate persistent builder-workspaces volume")
	}
}
