package builder_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
)

func TestMain(m *testing.M) {
	name := filepath.Base(os.Args[0])
	// These child modes only implement the fixture protocol. Skip the race
	// runtime's one-second shutdown delay for every simulated tool invocation;
	// the parent test process keeps its normal race-detector shutdown below.
	if name == "codex" || name == "codex.exe" || name == "go" || name == "go.exe" {
		syscall.Exit(fakes.RunBuild(strings.TrimSuffix(name, ".exe")))
	}
	if (name == "git" || name == "git.exe") && len(os.Args) > 1 && os.Args[1] == "-c" {
		syscall.Exit(fakes.RunGit())
	}
	os.Exit(m.Run())
}
