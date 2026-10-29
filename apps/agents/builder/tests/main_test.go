package builder_test

import (
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMain(m *testing.M) {
	name := filepath.Base(os.Args[0])
	if (name == "git" || name == "git.exe") && len(os.Args) > 1 && os.Args[1] == "-c" {
		syscall.Exit(fakes.RunGit())
	}
	os.Exit(m.Run())
}
