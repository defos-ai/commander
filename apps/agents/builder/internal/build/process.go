package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder/internal/process"
)

func Environment(scratch, codexExecutable, goExecutable string) ([]string, error) {
	paths := map[string]string{
		"HOME": "home", "USERPROFILE": "home", "XDG_CONFIG_HOME": "config", "CODEX_HOME": "codex",
		"TMPDIR": "tmp", "TMP": "tmp", "TEMP": "tmp", "GOTMPDIR": "tmp",
		"GOPATH": "go", "GOMODCACHE": "modules", "GOCACHE": "cache",
	}
	env := []string{
		"PATH=" + filepath.Dir(goExecutable) + string(os.PathListSeparator) + filepath.Dir(codexExecutable) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
	}
	for key, name := range paths {
		path := filepath.Join(scratch, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, errors.New("prepare temporary build environment failed")
		}
		env = append(env, key+"="+path)
	}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env, nil
}

func RunCodex(ctx context.Context, executable, source, scratch string, env []string, key []byte, instructions string) error {
	// Only the Codex parent receives its API key. Its generated shell commands
	// use the clean build environment, as do the independent Go checks below.
	settings := make([]string, 0, len(env))
	for _, setting := range env {
		name, value, _ := strings.Cut(setting, "=")
		settings = append(settings, strconv.Quote(name)+"="+strconv.Quote(value))
	}
	args := []string{"exec", "--sandbox", "workspace-write", "--ephemeral", "--color", "never",
		"--add-dir", scratch, "-c", `approval_policy="never"`,
		"-c", "sandbox_workspace_write.network_access=true", "-c", `shell_environment_policy.inherit="none"`,
		"-c", "shell_environment_policy.set={" + strings.Join(settings, ",") + "}", "-"}
	prompt := `Build the requested Commander agent in this existing example-agent checkout.
Follow AGENTS.md and preserve the template's framework/provider contracts, license notices,
CLI bootstrap, release packaging and external tests/ layout. Keep the ./cmd executable.
Implement real actions, schemas, discovery, tests, README and a matching mission.
Choose a distinct lowercase AgentType and replace the example-agent module and
identity consistently in Go imports, CLI, Dockerfile and documentation. Keep the
release helper's metadata/manifest commands, agent.yaml.tmpl and release workflows.
Preserve make-release.yml's version, expected_commit and request_id inputs, request
run-name, first-step commit guard and prepare dependencies. Every checkout uses github.sha.
Do not leave checkout-local replace directives in go.mod. The host will check the
generated release structure locally using temporary assets; it will not publish.
Run ordinary Go tests and fix failures. Do not run tagged integration tests, contact
live application services, publish, push, deploy or start the agent. Do not read or
write credentials, environment files, or authentication configuration. Use only
this checkout for source files. Never include secret values in source or output.
The host will independently run go test, go build and release structure checks,
then archive the source.

Requested agent:
` + instructions
	return RunCommand(ctx, "Codex", executable, source, append(append([]string(nil), env...), "CODEX_API_KEY="+string(key)), strings.NewReader(prompt), args...)
}

func RunCommand(ctx context.Context, stage, executable, source string, env []string, stdin io.Reader, args ...string) error {
	return runCommand(ctx, stage, executable, source, env, stdin, io.Discard, args...)
}

// CommandOutput reads bounded structured metadata without exposing tool output
// in errors. The caller still validates every returned field before using it.
func CommandOutput(ctx context.Context, stage, executable, source string, env []string, args ...string) ([]byte, error) {
	var output commandOutput
	if err := runCommand(ctx, stage, executable, source, env, nil, &output, args...); err != nil {
		return nil, err
	}
	if output.overflow {
		return nil, fmt.Errorf("%s output exceeds 64 KiB; fix its structured output", stage)
	}
	return bytes.Clone(output.buffer.Bytes()), nil
}

func runCommand(ctx context.Context, stage, executable, source string, env []string, stdin io.Reader, stdout io.Writer, args ...string) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir, command.Env, command.Stdin = source, env, stdin
	// Tool diagnostics can contain source, model output or credentials. Keep them
	// out of action errors and logs; a failed workspace remains available to fix.
	command.Stdout, command.Stderr = stdout, io.Discard
	command.WaitDelay = time.Second
	process.Configure(command)
	err := command.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Errorf("%s failed with exit status %d; workspace preserved for revision", stage, exit.ExitCode())
	}
	if err != nil {
		return fmt.Errorf("%s could not start; check its installation and run discovery again", stage)
	}
	return nil
}

type commandOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *commandOutput) Write(data []byte) (int, error) {
	count := len(data)
	remaining := (64 << 10) - b.buffer.Len()
	if count > remaining {
		b.overflow = true
		data = data[:remaining]
	}
	_, _ = b.buffer.Write(data)
	return count, nil
}
