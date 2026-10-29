package release_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	fixtures "github.com/defos-ai/commander/.github/tests/fixtures"
)

func TestCIComposeSecretsInitialization(t *testing.T) {
	f := fixtures.NewComposeSecrets(t)

	err := f.Run(t, "secrets-key", "")

	if err != nil {
		t.Fatalf("key generation failed: %v", err)
	}
	saved := f.Read(t, "secrets/key")
	key := strings.TrimSpace(saved)
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		t.Fatal("generated key must encode 32 bytes as hexadecimal")
	}
	for path, mode := range map[string]os.FileMode{"secrets": 0750, "secrets/key": 0440} {
		info, err := os.Stat(filepath.Join(f.Dir(), path))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s must be readable by the consumer group and inaccessible to others", path)
		}
	}
	for _, override := range []string{"", key, strings.ToUpper(key)} {
		if err := f.Run(t, "secrets-key", override); err != nil {
			t.Fatalf("saved key reuse failed: %v", err)
		}
		if f.Read(t, "secrets/key") != saved {
			t.Fatal("restart changed the persisted key bytes")
		}
	}
	entries, err := os.ReadDir(f.State())
	if err != nil || len(entries) != 1 || entries[0].Name() != "key" {
		t.Fatal("key initialization left temporary files")
	}
}

func TestCIComposeSecretsExplicitKey(t *testing.T) {
	f := fixtures.NewComposeSecrets(t)
	configured := strings.Repeat("aB", 32)
	if err := f.Run(t, "secrets-key", configured); err != nil {
		t.Fatalf("explicit key initialization failed: %v", err)
	}
	saved := f.Read(t, "secrets/key")
	if !strings.EqualFold(strings.TrimSpace(saved), configured) {
		t.Fatal("explicit key was not persisted")
	}
	if err := f.Run(t, "secrets-key", strings.ToLower(configured)); err != nil {
		t.Fatalf("equivalent hexadecimal override failed: %v", err)
	}
	for _, override := range []string{strings.Repeat("cd", 32), "invalid-key"} {
		if err := f.Run(t, "secrets-key", override); err == nil {
			t.Fatal("conflicting or invalid override was accepted")
		}
		if f.Read(t, "secrets/key") != saved {
			t.Fatal("override changed the persisted key bytes")
		}
	}
}

func TestCIComposeSecretsRejectsInvalidOverride(t *testing.T) {
	for _, override := range []string{strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("g", 64), strings.Repeat("a", 64) + "\n"} {
		f := fixtures.NewComposeSecrets(t)
		if err := f.Run(t, "secrets-key", override); err == nil {
			t.Fatal("override must contain exactly 64 hexadecimal characters")
		}
		if _, err := os.Stat(filepath.Join(f.State(), "key")); !os.IsNotExist(err) {
			t.Fatal("invalid override created a persisted key")
		}
	}
}

func TestCIComposeSecretsRejectsInvalidState(t *testing.T) {
	for _, kind := range []string{"empty", "short", "nonhex", "directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			f := fixtures.NewComposeSecrets(t)
			path := filepath.Join(f.State(), "key")
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
				if err = os.WriteFile(filepath.Join(f.Dir(), "outside"), []byte(strings.Repeat("ab", 32)), 0600); err == nil {
					err = os.Symlink(filepath.Join(f.Dir(), "outside"), path)
				}
			case "dangling symlink":
				err = os.Symlink(filepath.Join(f.Dir(), "absent"), path)
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
				if err := f.Run(t, "secrets-key", override); err == nil {
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

func TestCIComposeSecretsConsumer(t *testing.T) {
	for _, service := range []string{"api", "controller"} {
		t.Run(service, func(t *testing.T) { testComposeSecretsConsumer(t, service) })
	}
}

func testComposeSecretsConsumer(t *testing.T, service string) {
	f := fixtures.NewComposeSecrets(t)
	if err := f.Run(t, "secrets-key", strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("key initialization failed: %v", err)
	}
	var arguments []string
	if service == "controller" {
		arguments = []string{"--task-timeout=30m"}
	}
	if err := f.Run(t, service, strings.Repeat("cd", 32), arguments...); err != nil {
		t.Fatalf("consumer startup failed: %v", err)
	}
	if service != "api" && f.Read(t, "observed-arguments") != strings.Join(arguments, "\n")+"\n" {
		t.Fatal("consumer key initialization dropped startup arguments")
	}
	if f.Read(t, "observed-key") != strings.TrimSpace(f.Read(t, "secrets/key")) {
		t.Fatal("consumer must load the persisted key even when its environment contains an override")
	}
	if f.Read(t, "observed-password") != "configured-api-password" {
		t.Fatal("consumer startup changed its configured password")
	}
	if f.Read(t, "before-pid") != f.Read(t, "after-pid") {
		t.Fatal("consumer startup did not replace the shell process")
	}
	for _, state := range []string{"missing", "empty"} {
		t.Run(state, func(t *testing.T) {
			f := fixtures.NewComposeSecrets(t)
			if state == "empty" {
				if err := os.WriteFile(filepath.Join(f.State(), "key"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.Run(t, service, strings.Repeat("ab", 32)); err == nil {
				t.Fatal("consumer started without a nonempty persisted key")
			}
			if _, err := os.Stat(filepath.Join(f.Dir(), "observed-key")); !os.IsNotExist(err) {
				t.Fatal("consumer launched before loading its persisted key")
			}
		})
	}
}

func TestCIComposeSecretsWiring(t *testing.T) {
	services := fixtures.ComposeServices(t)
	setup := services["secrets-key"]
	if setup.Image != services["api"].Image || setup.User != "0:1000" || !setup.ReadOnly ||
		setup.NetworkMode != "none" || !slices.Contains(setup.CapDrop, "ALL") ||
		!slices.Contains(setup.SecurityOpt, "no-new-privileges:true") {
		t.Fatal("key initialization must reuse the API image with restricted root/group access and no network")
	}
	if !slices.Equal(setup.Volumes, []string{"secrets:/run/commander-secrets"}) ||
		setup.Environment["SECRETS_ENCRYPTION_KEY"] != "${SECRETS_ENCRYPTION_KEY:-}" {
		t.Fatal("key initialization must own its writable volume and accept the optional explicit key")
	}
	api := services["api"]
	if api.Init == nil || *api.Init {
		t.Fatal("API must remain PID 1")
	}
	for _, name := range []string{"api", "controller"} {
		service := services[name]
		if !slices.Contains(service.Volumes, "secrets:/run/commander-secrets:ro") ||
			service.DependsOn["secrets-key"].Condition != "service_completed_successfully" {
			t.Fatalf("%s must wait for key initialization and mount its key read-only", name)
		}
	}
	for name, service := range services {
		if name == "secrets-key" {
			continue
		}
		if _, exists := service.Environment["SECRETS_ENCRYPTION_KEY"]; exists {
			t.Fatalf("%s must not receive the key through Compose environment configuration", name)
		}
		if name == "api" || name == "controller" {
			continue
		}
		assertComposeServiceHasNoEncryptionKey(t, name, service)
	}
	var compose struct {
		Volumes     map[string]any
		Environment map[string]string `yaml:"x-environment"`
	}
	readCIYAML(t, "../..", "compose.yaml", &compose)
	if _, exists := compose.Environment["SECRETS_ENCRYPTION_KEY"]; exists {
		t.Fatal("shared Compose environment must not contain the secrets encryption key")
	}
	if _, exists := compose.Volumes["secrets"]; !exists {
		t.Fatal("Compose must declare a persistent secrets volume")
	}
	builder := services["builder-agent"]
	if _, exists := compose.Volumes["builder-state"]; !exists ||
		!slices.Contains(builder.Volumes, "builder-state:/home/commander/workspaces") {
		t.Fatal("Compose builder must retain workspaces across container recreation")
	}
	if builder.User != "1000:1000" || !slices.Contains(builder.CapDrop, "ALL") ||
		!slices.Contains(builder.SecurityOpt, "no-new-privileges:true") {
		t.Fatal("Compose builder must use its unprivileged image user without extra privileges")
	}
}

func TestCIComposeTestStacksRestrictEncryptionKeyRecipients(t *testing.T) {
	for _, file := range []string{"compose.test.yaml", "compose.full.test.yaml"} {
		t.Run(file, func(t *testing.T) {
			var compose struct {
				Services    map[string]fixtures.ComposeService
				Environment map[string]string `yaml:"x-environment"`
			}
			readCIYAML(t, "../composes", file, &compose)
			if _, exists := compose.Environment["SECRETS_ENCRYPTION_KEY"]; exists {
				t.Fatal("shared test environment must not contain the secrets encryption key")
			}
			for _, name := range []string{"api", "controller"} {
				if compose.Services[name].Environment["SECRETS_ENCRYPTION_KEY"] == "" {
					t.Fatalf("%s must retain its isolated fixture encryption key", name)
				}
			}
			for name, service := range compose.Services {
				if name != "api" && name != "controller" {
					assertComposeServiceHasNoEncryptionKey(t, name, service)
				}
			}
		})
	}
}

func assertComposeServiceHasNoEncryptionKey(t *testing.T, name string, service fixtures.ComposeService) {
	t.Helper()
	if _, exists := service.Environment["SECRETS_ENCRYPTION_KEY"]; exists {
		t.Fatalf("%s must not receive the secrets encryption key", name)
	}
	if _, exists := service.DependsOn["secrets-key"]; exists {
		t.Fatalf("%s must not depend directly on secrets-key initialization", name)
	}
	for _, volume := range service.Volumes {
		if strings.HasPrefix(volume, "secrets:") || strings.Contains(volume, ":/run/commander-secrets") {
			t.Fatalf("%s must not mount the secrets encryption key", name)
		}
	}
	for _, argument := range append(slices.Clone(service.Entrypoint), service.Command...) {
		if strings.Contains(argument, "SECRETS_ENCRYPTION_KEY") || strings.Contains(argument, "/run/commander-secrets") {
			t.Fatalf("%s startup must not read the secrets encryption key", name)
		}
	}
}
