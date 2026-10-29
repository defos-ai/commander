package fakes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
)

type GitConfig struct {
	Source  string
	Mode    string
	Started string
	Publish *PublicationGit
}

type GitState struct {
	GitConfig
	RealGit string
}

func RunGit() int {
	if err := executeGit(); err != nil {
		fmt.Fprintln(os.Stderr, "Git fixture:", err)
		return 7
	}
	return 0
}

func executeGit() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	encoded, err := os.ReadFile(filepath.Join(filepath.Dir(self), "fixture.json"))
	if err != nil {
		return err
	}
	var config GitState
	if err := json.Unmarshal(encoded, &config); err != nil {
		return err
	}
	if config.Publish != nil {
		return executePublicationGit(config.RealGit, *config.Publish)
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
