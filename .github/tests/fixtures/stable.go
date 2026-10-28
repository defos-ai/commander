package fixtures

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	fakes "github.com/defos-ai/commander/.github/tests/fakes"
)

var stableImageNames = []string{
	"commander-admin", "commander-aio", "commander-api",
	"commander-builder-agent", "commander-controller", "commander-mcp", "commander-playground",
	"commander-spawner-agent",
}

type Stable struct {
	dir, selectRun, promoteRun string
	env                        map[string]string
	images                     map[string]string
}

func NewStable(t *testing.T) *Stable {
	t.Helper()
	f := &Stable{dir: t.TempDir(), images: map[string]string{}}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ ID, Run string }
		}
	}
	readYAML(t, "..", "workflows/push-stable.yml", &workflow)
	for _, step := range workflow.Jobs["promote"].Steps {
		switch step.ID {
		case "select":
			f.selectRun = step.Run
		case "promote":
			f.promoteRun = step.Run
		}
	}
	if f.selectRun == "" || f.promoteRun == "" {
		t.Fatal("stable workflow is missing selection or promotion script")
	}
	for i, name := range stableImageNames {
		f.images[name] = fmt.Sprintf("sha256:%064x", i+1)
	}
	f.env = map[string]string{
		"PATH": f.dir + ":/usr/bin:/bin", "STATE": f.dir,
		"VERSION": "v1.10.0", "GH_REPO": "defos-ai/commander",
		"GITHUB_OUTPUT":       filepath.Join(f.dir, "output"),
		"GITHUB_STEP_SUMMARY": filepath.Join(f.dir, "summary"),
		"RELEASES":            `[{"tag_name":"v1.10.0","draft":false,"prerelease":false}]`,
	}
	f.Write(t, "gh", fakes.StableGitHub)
	f.Write(t, "docker", fakes.StableDocker)
	return f
}

func (f *Stable) Write(t *testing.T, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(data), 0700); err != nil {
		t.Fatal(err)
	}
}

func (f *Stable) Read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func (f *Stable) run(t *testing.T, script string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script)
	images, err := json.Marshal(f.images)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = []string{"IMAGES=" + string(images)}
	for key, value := range f.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func StableImageNames() []string { return append([]string(nil), stableImageNames...) }
func (f *Stable) Select(t *testing.T) (string, error) {
	t.Helper()
	return f.run(t, f.selectRun)
}
func (f *Stable) Promote(t *testing.T) (string, error) {
	t.Helper()
	return f.run(t, f.promoteRun)
}
func (f *Stable) SetEnvironment(key, value string) { f.env[key] = value }
func (f *Stable) Environment(key string) string    { return f.env[key] }
func (f *Stable) SetImage(name, digest string)     { f.images[name] = digest }
func (f *Stable) Image(name string) string         { return f.images[name] }
func (f *Stable) RemoveImage(name string)          { delete(f.images, name) }
