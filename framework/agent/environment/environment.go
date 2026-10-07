package environment

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
)

type DiscoveryScope = agentmodel.DiscoveryScope

type Result struct {
	ReturnCode int    `json:"return_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
}

type Environment interface {
	LookupEnv(string) (string, bool)
	FindExecutable(context.Context, string, *DiscoveryScope) (string, error)
	Run(context.Context, []string) (Result, error)
}

// Local runs argument vectors directly in the agent's own process environment.
// Configuration must be set before concurrent use. It does not invoke a shell.
type Local struct {
	SearchPaths      []string
	WorkingDirectory string
}

// LookupEnv reads deployment configuration without exposing it in agent metadata.
func (*Local) LookupEnv(name string) (string, bool) { return os.LookupEnv(name) }

func (local *Local) FindExecutable(ctx context.Context, name string, scope *DiscoveryScope) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return "", errors.New("executable name must be a filename")
	}
	paths := local.SearchPaths
	if scope != nil && scope.Paths != nil {
		paths = scope.Paths
	}
	if paths == nil {
		paths = filepath.SplitList(os.Getenv("PATH"))
	}
	for _, directory := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if directory == "" {
			continue
		}
		if !filepath.IsAbs(directory) && local.WorkingDirectory != "" {
			directory = filepath.Join(local.WorkingDirectory, directory)
		}
		directory, err := filepath.Abs(directory)
		if err != nil {
			return "", err
		}
		candidate := filepath.Join(directory, name)
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			continue
		}
		if scope != nil && scope.Paths != nil {
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				continue
			}
			root, err := filepath.EvalSymlinks(directory)
			if err != nil {
				continue
			}
			relative, err := filepath.Rel(root, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			candidate = resolved
		}
		return candidate, nil
	}
	return "", nil
}

func (local *Local) Run(ctx context.Context, argv []string) (Result, error) {
	if len(argv) == 0 || argv[0] == "" {
		return Result{}, errors.New("argv must contain an executable")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = local.WorkingDirectory
	command.WaitDelay = time.Second
	configureCommand(command)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if command.ProcessState != nil {
		result.ReturnCode = command.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return result, nil
	}
	return result, err
}
