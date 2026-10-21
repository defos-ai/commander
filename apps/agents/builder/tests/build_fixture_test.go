package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/environment"
)

const buildTestSecret = "managed-codex-token-not-for-artifacts"
const buildTestInstructions = "Create an agent that counts words. $(never-run-this)"

type buildFixtureConfig struct {
	Source, Mode, Started, Calls, Home string
}

func buildFixtureScope(t *testing.T, config buildFixtureConfig) *environment.DiscoveryScope {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, name := range []string{"codex", "go"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(bin, name)
		if runtime.GOOS == "windows" {
			if err := copyGitFixture(self, path); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Link(self, path); err != nil {
			if err := copyGitFixture(self, path); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "build-fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return &environment.DiscoveryScope{Paths: []string{bin}}
}

func runBuildFixture(tool string) int {
	if err := executeBuildFixture(tool); err != nil {
		fmt.Fprintln(os.Stderr, "Build fixture:", err)
		return 7
	}
	return 0
}

func executeBuildFixture(tool string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(self), "build-fixture.json"))
	if err != nil {
		return err
	}
	var config buildFixtureConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	actual, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(config.Source)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("child ran outside the selected source directory")
	}
	for _, key := range []string{"GITHUB_TOKEN", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD", "COMMANDER_MCP_TOKEN", "OPENAI_API_KEY"} {
		if _, exists := os.LookupEnv(key); exists {
			return fmt.Errorf("child inherited %s", key)
		}
	}
	if os.Getenv("HOME") == "" {
		return errors.New("missing private child home")
	}
	if tool == "codex" {
		if os.Getenv("CODEX_HOME") == "" {
			return errors.New("Codex did not receive isolated configuration storage")
		}
		if os.Getenv("CODEX_API_KEY") != buildTestSecret {
			return errors.New("Codex did not receive its managed key")
		}
		args := os.Args[1:]
		if len(args) == 0 || args[0] != "exec" || args[len(args)-1] != "-" {
			return errors.New("Codex was not given a stdin prompt")
		}
		if !slices.Contains(args, "--ephemeral") {
			return errors.New("Codex session must be ephemeral")
		}
		if index := slices.Index(args, "--sandbox"); index < 0 || index+1 >= len(args) || args[index+1] != "workspace-write" {
			return errors.New("Codex sandbox was not scoped to the workspace")
		}
		prompt, err := io.ReadAll(io.LimitReader(os.Stdin, 128<<10))
		if err != nil {
			return err
		}
		if !strings.Contains(string(prompt), buildTestInstructions) || strings.Contains(string(prompt), buildTestSecret) {
			return errors.New("stdin lost instructions or included credentials")
		}
		if config.Home != "" {
			if err := os.WriteFile(config.Home, []byte(os.Getenv("HOME")), 0600); err != nil {
				return err
			}
		}
	} else {
		if _, exists := os.LookupEnv("CODEX_API_KEY"); exists {
			return errors.New("Go inherited the model key")
		}
		if os.Getenv("GOWORK") != "off" {
			return errors.New("Go inherited workspace module wiring")
		}
	}
	if err := appendBuildCall(config.Calls, tool+" "+strings.Join(os.Args[1:], " ")); err != nil {
		return err
	}
	if tool == "codex" {
		switch config.Mode {
		case "wait":
			if err := os.WriteFile(config.Started, []byte("started"), 0600); err != nil {
				return err
			}
			time.Sleep(30 * time.Second)
			return errors.New("cancelled child kept running")
		case "fail":
			fmt.Fprintln(os.Stdout, buildTestSecret)
			return errors.New("untrusted-sensitive-build-diagnostic: " + buildTestSecret)
		}
		if err := os.MkdirAll(filepath.Join(cwd, "cmd"), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cwd, "cmd", "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
			return err
		}
		if config.Mode == "leak" {
			return os.WriteFile(filepath.Join(cwd, "leaked.txt"), []byte(buildTestSecret), 0600)
		}
		if config.Mode == "filename-leak" {
			return os.WriteFile(filepath.Join(cwd, buildTestSecret+".txt"), []byte("clean contents"), 0600)
		}
		return nil
	}
	args := os.Args[1:]
	if slices.Equal(args, []string{"test", "-count=1", "-timeout=2m", "./..."}) {
		if config.Mode == "test-fail" {
			return errors.New("untrusted-sensitive-build-diagnostic: " + buildTestSecret)
		}
		return nil
	}
	if len(args) == 4 && args[0] == "build" && args[1] == "-o" && args[3] == "./cmd" {
		if config.Mode == "build-fail" {
			return errors.New("untrusted-sensitive-build-diagnostic: " + buildTestSecret)
		}
		if relative, err := filepath.Rel(config.Source, args[2]); err != nil || filepath.IsLocal(relative) {
			return errors.New("validation binary must stay outside source")
		}
		return os.WriteFile(args[2], []byte("fixture executable"), 0700)
	}
	return errors.New("unexpected Go verification command")
}

func appendBuildCall(path, value string) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	_, err = fmt.Fprintln(file, value)
	return err
}
