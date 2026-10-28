package fixtures

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func Template(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	Git(t, "init", "--quiet", directory)
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("Local example-agent fixture.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	Git(t, "-C", directory, "add", "README.md")
	Git(t, "-C", directory, "-c", "user.name=Builder fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "Template fixture")
	return directory, strings.TrimSpace(Git(t, "-C", directory, "rev-parse", "HEAD"))
}

func Git(t *testing.T, args ...string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("Git is required for local checkout fixtures:", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, git, args...)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	}
	if value := os.Getenv("SystemRoot"); value != "" {
		command.Env = append(command.Env, "SystemRoot="+value)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture Git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
