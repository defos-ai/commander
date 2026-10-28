package fixtures

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

	fakes "github.com/defos-ai/commander/.github/tests/fakes"
)

type ComposeSecrets struct {
	dir, state string
	services   map[string]ComposeService
}

func NewComposeSecrets(t *testing.T) *ComposeSecrets {
	t.Helper()
	f := &ComposeSecrets{dir: t.TempDir(), services: ComposeServices(t)}
	f.state = filepath.Join(f.dir, "secrets")
	if err := os.Mkdir(f.state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.dir, "api-credentials"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "api-credentials/api-password"), []byte("configured-api-password"), 0600); err != nil {
		t.Fatal(err)
	}
	mock := fakes.ComposeSecretsConsumer
	for _, binary := range []string{"commander-api", "commander-controller", "commander-spawner-agent", "commander-builder-agent"} {
		if err := os.WriteFile(filepath.Join(f.dir, binary), []byte(mock), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *ComposeSecrets) Run(t *testing.T, service, key string, arguments ...string) error {
	t.Helper()
	spec := f.services[service]
	var script string
	if slices.Equal(spec.Entrypoint, []string{"/bin/sh", "-ec"}) && len(spec.Command) == 1 {
		script = spec.Command[0]
	} else if len(spec.Entrypoint) == 4 && slices.Equal(spec.Entrypoint[:2], []string{"/bin/sh", "-ec"}) {
		script = spec.Entrypoint[2]
	} else {
		t.Fatal("secrets initialization and consumer startup must define one shell command")
	}
	script = strings.ReplaceAll(script, "$$", "$")
	script = strings.ReplaceAll(script, "/run/commander-secrets", f.state)
	script = strings.ReplaceAll(script, "/run/commander-api", filepath.Join(f.dir, "api-credentials"))
	// The local fixture uses the runner's group instead of the container group.
	script = strings.ReplaceAll(script, "chgrp 1000", "chgrp "+strconv.Itoa(os.Getgid()))
	if service != "secrets-key" {
		script = "printf '%s' \"$$\" > \"$FIXTURE_DIR/before-pid\"\n" + script
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", append([]string{"-ec", script, service}, arguments...)...)
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

func (f *ComposeSecrets) Read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *ComposeSecrets) Dir() string   { return f.dir }
func (f *ComposeSecrets) State() string { return f.state }
