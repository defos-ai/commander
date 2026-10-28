package fixtures

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakes "github.com/defos-ai/commander/.github/tests/fakes"
)

var commanderBinaryNames = []string{
	"commander_linux_amd64", "commander_linux_arm64",
	"commander_darwin_amd64", "commander_darwin_arm64",
	"commander_windows_amd64.exe", "commander_windows_arm64.exe",
}

var rootBinaryNames = append(append([]string{}, commanderBinaryNames...),
	"commander-admin_linux_amd64", "commander-admin_linux_arm64",
	"commander-admin_darwin_amd64", "commander-admin_darwin_arm64",
	"commander-admin_windows_amd64.exe", "commander-admin_windows_arm64.exe",
)

var nativeBinaryNames = append(append([]string{}, rootBinaryNames...),
	"commander-spawner-agent_linux_amd64", "commander-spawner-agent_linux_arm64",
	"commander-spawner-agent_darwin_amd64", "commander-spawner-agent_darwin_arm64",
	"commander-spawner-agent_windows_amd64.exe", "commander-spawner-agent_windows_arm64.exe",
	"commander-builder-agent_linux_amd64", "commander-builder-agent_linux_arm64",
	"commander-builder-agent_darwin_amd64", "commander-builder-agent_darwin_arm64",
	"commander-builder-agent_windows_amd64.exe", "commander-builder-agent_windows_arm64.exe",
)

func AgentNativeBinaryNames(agent string) []string {
	var names []string
	for _, name := range nativeBinaryNames {
		if strings.HasPrefix(name, "commander-"+agent+"-agent_") {
			names = append(names, name)
		}
	}
	return names
}

type Native struct {
	dir, artifacts, script string
	env                    map[string]string
}

func NewNative(t *testing.T) *Native {
	t.Helper()
	f := &Native{dir: t.TempDir()}
	f.artifacts = filepath.Join(f.dir, "artifacts")
	if err := os.Mkdir(f.artifacts, 0700); err != nil {
		t.Fatal(err)
	}
	f.script = WorkflowScript(t, "push-binaries.yml", "release", "publish")
	f.env = map[string]string{
		"PATH": f.dir + ":/usr/bin:/bin", "STATE": f.dir,
		"VERSION": "v0.1.0", "GH_REPO": "defos-ai/commander",
		"RELEASE_TAG": "v0.1.0", "RELEASE_TITLE": "Commander v0.1.0",
		"RELEASES": "", "GH_STATUS": "0",
	}
	f.Write(t, "LICENSE", "Commander test license\n")
	for _, name := range nativeBinaryNames {
		f.Write(t, "artifacts/"+name, name+"\n")
	}
	f.Write(t, "gh", fakes.NativeGitHub)
	return f
}

func WorkflowScript(t *testing.T, file, job, stepID string) string {
	t.Helper()
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ ID, Run string }
		}
	}
	readYAML(t, "..", "workflows/"+file, &workflow)
	for _, step := range workflow.Jobs[job].Steps {
		if step.ID == stepID && step.Run != "" {
			return step.Run
		}
	}
	t.Fatalf("%s lacks %s step %s", file, job, stepID)
	return ""
}

func (f *Native) Write(t *testing.T, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(data), 0700); err != nil {
		t.Fatal(err)
	}
}

func (f *Native) Read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func (f *Native) Run(t *testing.T) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", f.script)
	cmd.Dir = f.artifacts
	for key, value := range f.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func CommanderBinaryNames() []string               { return append([]string(nil), commanderBinaryNames...) }
func RootBinaryNames() []string                    { return append([]string(nil), rootBinaryNames...) }
func NativeBinaryNames() []string                  { return append([]string(nil), nativeBinaryNames...) }
func (f *Native) Dir() string                      { return f.dir }
func (f *Native) Artifacts() string                { return f.artifacts }
func (f *Native) SetArtifacts(path string)         { f.artifacts = path }
func (f *Native) SetScript(script string)          { f.script = script }
func (f *Native) SetEnvironment(key, value string) { f.env[key] = value }
func (f *Native) Environment(key string) string    { return f.env[key] }

func NewNativeBuild(t *testing.T, platform string) *Native {
	t.Helper()
	f := &Native{dir: t.TempDir()}
	f.artifacts = f.dir
	f.script = WorkflowScript(t, "build-binaries.yml", "build", "build")
	goos, goarch, _ := strings.Cut(platform, "_")
	f.env = map[string]string{
		"PATH": f.dir + ":/usr/bin:/bin", "STATE": f.dir,
		"GOOS": goos, "GOARCH": goarch, "VERSION": "v0.1.0",
	}
	f.Write(t, "go", fakes.NativeGoBuild)

	return f
}
