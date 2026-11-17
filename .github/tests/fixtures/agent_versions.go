package fixtures

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	fakes "github.com/defos-ai/commander/.github/tests/fakes"
)

func BuildAgentVersions(t *testing.T, builderVersion, spawnerVersion string) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "agent-versions")
	flags := "-X github.com/defos-ai/commander/apps/agents/builder.Version=" + builderVersion +
		" -X github.com/defos-ai/commander/apps/agents/spawner.Version=" + spawnerVersion
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-ldflags="+flags, "-o", binary,
		"./.github/tests/fixtures/agent_versions/main.go")
	command.Dir = root
	// The named fixture compiles against the release workspace, outside the CI module.
	command.Env = append(os.Environ(), "GOWORK="+filepath.Join(root, "go.work"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build embedded agent version fixture: %v: %s", err, output)
	}
	return binary
}

func NewAgentImageBuild(t *testing.T, script, version string) *Native {
	t.Helper()
	f := &Native{dir: t.TempDir(), script: script}
	f.artifacts = f.dir
	f.env = map[string]string{
		"PATH": f.dir + ":/usr/bin:/bin", "STATE": f.dir, "VERSION": version,
	}
	f.Write(t, "go", fakes.ImageGoBuild)
	return f
}
