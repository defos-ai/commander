package builder_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/framework/agent/environment"
)

type gitFixtureConfig struct {
	Source  string
	Mode    string
	Started string
}

type gitFixtureState struct {
	gitFixtureConfig
	RealGit string
}

func TestMain(m *testing.M) {
	name := filepath.Base(os.Args[0])
	if name == "codex" || name == "codex.exe" || name == "go" || name == "go.exe" {
		os.Exit(runBuildFixture(strings.TrimSuffix(name, ".exe")))
	}
	if (name == "git" || name == "git.exe") && len(os.Args) > 1 && os.Args[1] == "-c" {
		os.Exit(runGitFixture())
	}
	os.Exit(m.Run())
}

// A discovered executable provides the fixture boundary without adding a
// configurable template URL or test-only hooks to the production action.
func gitFixtureScope(t *testing.T, config gitFixtureConfig) *environment.DiscoveryScope {
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
		if err := copyGitFixture(self, executable); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Link(self, executable); err != nil {
		if err := copyGitFixture(self, executable); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(gitFixtureState{gitFixtureConfig: config, RealGit: realGit})
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

func copyGitFixture(source, destination string) (err error) {
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

func runGitFixture() int {
	if err := executeGitFixture(); err != nil {
		fmt.Fprintln(os.Stderr, "Git fixture:", err)
		return 7
	}
	return 0
}

func executeGitFixture() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	encoded, err := os.ReadFile(filepath.Join(filepath.Dir(self), "fixture.json"))
	if err != nil {
		return err
	}
	var config gitFixtureState
	if err := json.Unmarshal(encoded, &config); err != nil {
		return err
	}
	if os.Getenv("GIT_ALLOW_PROTOCOL") != "https" {
		return errors.New("checkout did not restrict the public template to HTTPS")
	}
	args := append([]string(nil), os.Args[1:]...)
	index := 0
	for index+1 < len(args) && args[index] == "-c" {
		index += 2
	}
	if index >= len(args) {
		return errors.New("missing Git command")
	}
	switch args[index] {
	case "clone":
		if len(args)-index < 3 || args[len(args)-2] != builderactions.TemplateURL {
			return errors.New("checkout did not request the fixed public template")
		}
	case "rev-parse":
		if strings.Join(args[index:], " ") != "rev-parse --verify HEAD^{commit}" {
			return errors.New("unexpected revision request")
		}
	default:
		return errors.New("unexpected Git command")
	}
	switch config.Mode {
	case "", "check-secrets":
		if config.Mode == "check-secrets" {
			for _, key := range []string{"GITHUB_TOKEN", "SECRETS_ENCRYPTION_KEY"} {
				if _, exists := os.LookupEnv(key); exists {
					return fmt.Errorf("inherited host secret %s", key)
				}
			}
		}
	case "fail":
		return errors.New("untrusted-sensitive-diagnostic")
	case "overflow":
		_, err := io.WriteString(os.Stdout, strings.Repeat("0", 5000))
		return err
	case "wait":
		if err := os.WriteFile(config.Started, []byte("started"), 0600); err != nil {
			return err
		}
		time.Sleep(30 * time.Second)
		return errors.New("blocking Git fixture was not canceled")
	default:
		return errors.New("unknown Git fixture mode")
	}
	if args[index] == "clone" {
		if !filepath.IsAbs(config.Source) {
			return errors.New("fixture source must be an absolute local path")
		}
		args[len(args)-2] = config.Source
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, config.RealGit, args...)
	command.Env = append(os.Environ(), "GIT_ALLOW_PROTOCOL=file")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.WaitDelay = time.Second
	return command.Run()
}
