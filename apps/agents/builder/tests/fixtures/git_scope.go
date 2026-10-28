package fixtures

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/environment"
)

// A discovered executable provides the fixture boundary without adding a
// configurable template URL or test-only hooks to the production action.
func GitScope(t *testing.T, config fakes.GitConfig) *environment.DiscoveryScope {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("Git is required for local checkout fixtures:", err)
	}
	realGit, err = filepath.Abs(realGit)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable := filepath.Join(bin, name)
	// Windows locks the running test image; a copy lets each fixture be removed
	// independently of the parent process, including the missing-Git scenario.
	if runtime.GOOS == "windows" {
		if err := copyExecutable(self, executable); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Link(self, executable); err != nil {
		if err := copyExecutable(self, executable); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(fakes.GitState{GitConfig: config, RealGit: realGit})
	if err != nil {
		t.Fatal(err)
	}
	// The action intentionally strips arbitrary environment variables, so keep
	// fixture setup beside its executable rather than passing an environment flag.
	if err := os.WriteFile(filepath.Join(bin, "fixture.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return &environment.DiscoveryScope{Paths: []string{bin}}
}

func copyExecutable(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	_, err = io.Copy(output, input)
	return err
}
