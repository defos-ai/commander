package fixtures

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func BuildAgentManifestGenerator(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "agent-manifest")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "./.github/scripts/agent-manifest.go")
	cmd.Dir = root
	// CI tooling runs outside go.work; the generator uses the tested framework.
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(root, "go.work"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build manifest generator: %v: %s", err, output)
	}
	return binary
}
