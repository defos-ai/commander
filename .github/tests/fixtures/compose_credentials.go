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

type ComposeService struct {
	Image       string
	User        string
	Restart     string
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

func ComposeServices(t *testing.T) map[string]ComposeService {
	t.Helper()
	var compose struct {
		Services map[string]ComposeService
	}
	readYAML(t, "../..", "compose.yaml", &compose)
	return compose.Services
}

type ComposeCredentialKind struct {
	Name, Variable, PlaygroundVariable, File, Mount string
}

func ComposeCredentialKinds() []ComposeCredentialKind {
	return []ComposeCredentialKind{
		{"mcp", "MCP_TOKEN", "COMMANDER_MCP_TOKEN", "mcp-token", "/run/commander"},
		{"api", "API_PASSWORD", "COMMANDER_API_PASSWORD", "api-password", "/run/commander-api"},
	}
}

type ComposeCredential struct {
	dir, state string
	kind       ComposeCredentialKind
	services   map[string]ComposeService
}

func NewComposeCredential(t *testing.T, kind ComposeCredentialKind) *ComposeCredential {
	t.Helper()
	f := &ComposeCredential{dir: t.TempDir(), kind: kind, services: ComposeServices(t)}
	f.state = filepath.Join(f.dir, kind.Name+"-credentials")
	for _, directory := range []string{"mcp-credentials", "api-credentials", "secrets"} {
		if err := os.Mkdir(filepath.Join(f.dir, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.dir, "secrets/key"), []byte(strings.Repeat("ab", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, binary := range []string{"commander-mcp", "commander-api", "commander-admin", "commander"} {
		mock := fakes.ComposeCredentialsConsumer
		if err := os.WriteFile(filepath.Join(f.dir, binary), []byte(mock), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *ComposeCredential) Run(t *testing.T, service, credential string, arguments ...string) error {
	t.Helper()
	return f.RunWithEnvironment(t, service, credential, nil, arguments...)
}

func (f *ComposeCredential) RunWithEnvironment(t *testing.T, service, credential string, environment map[string]string, arguments ...string) error {
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
	if service == f.kind.Name+"-credentials" {
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
	variable := f.kind.Variable
	if service == "playground" || service == "missions" {
		variable = f.kind.PlaygroundVariable
	}
	values[variable] = credential
	for key, value := range environment {
		values[key] = value
	}
	for key, value := range values {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.CombinedOutput()
	saved, _ := os.ReadFile(filepath.Join(f.state, f.kind.File))
	for _, secret := range []string{credential, strings.TrimSpace(string(saved))} {
		if secret != "" && strings.Contains(string(output), secret) {
			t.Fatal("credential startup exposed a secret in its output")
		}
	}
	return err
}

func (f *ComposeCredential) Read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *ComposeCredential) Dir() string                        { return f.dir }
func (f *ComposeCredential) State() string                      { return f.state }
func (f *ComposeCredential) Service(name string) ComposeService { return f.services[name] }
