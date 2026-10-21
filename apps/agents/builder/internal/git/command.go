package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder/internal/process"
)

type Command struct {
	Executable string
	Home       string
}

func (g Command) Run(ctx context.Context, directory string, args ...string) (string, error) {
	// The template is public: do not inherit Git helpers, hooks, URL rewrites,
	// proxy settings or agent secrets. Never invoke a shell or recurse submodules.
	options := []string{
		"-c", "credential.helper=",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "submodule.recurse=false",
		"-c", "http.followRedirects=false",
	}
	command := exec.CommandContext(ctx, g.Executable, append(options, args...)...)
	command.Dir = directory
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + g.Home,
		"USERPROFILE=" + g.Home,
		"XDG_CONFIG_HOME=" + g.Home,
		"TMPDIR=" + g.Home,
		"TMP=" + g.Home,
		"TEMP=" + g.Home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=https",
		"LC_ALL=C",
	}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	command.WaitDelay = time.Second
	process.Configure(command)
	var output gitOutput
	command.Stdout = &output
	// Raw Git diagnostics can contain environment or server-supplied data. Return
	// only the exit status and a bounded revision, never stderr in task results.
	command.Stderr = io.Discard
	err := command.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return "", fmt.Errorf("Git exited with status %d; check Git installation and template access", exitError.ExitCode())
	}
	if err != nil {
		return "", err
	}
	if output.overflow {
		return "", errors.New("Git output exceeded its limit")
	}
	return output.buffer.String(), nil
}

type gitOutput struct {
	// Keep the buffer named: embedding it would expose ReadFrom and let io.Copy
	// bypass Write's size limit when os/exec drains the stdout pipe.
	buffer   bytes.Buffer
	overflow bool
}

func (b *gitOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 4096 - b.buffer.Len()
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}
