package fakes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func RunBuildTool() int {
	self, err := os.Executable()
	if err != nil {
		return 9
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(self), "fixture.json"))
	if err != nil {
		return 9
	}
	var config GitState
	if json.Unmarshal(data, &config) != nil {
		return 9
	}
	if strings.TrimSuffix(filepath.Base(self), ".exe") == "go" {
		return runLocalGo(self, config)
	}
	if os.Getenv("CODEX_API_KEY") != "workspace-model-key" {
		return 9
	}
	if config.BuildMode == "success" {
		if err := os.CopyFS(".", os.DirFS(config.BuildSource)); err != nil {
			return 9
		}
		return 0
	}
	if os.WriteFile("generated.txt", []byte("Partial generated source is retained.\n"), 0600) != nil {
		return 9
	}
	if config.BuildMode == "wait" {
		if os.WriteFile(config.BuildStarted, []byte("started"), 0600) != nil {
			return 9
		}
		time.Sleep(30 * time.Second)
	}
	return 9
}

func runLocalGo(self string, config GitState) int {
	if config.RealGo == "" || os.Getenv("CODEX_API_KEY") != "" || os.Getenv("OPENAI_API_KEY") != "" {
		return 9
	}
	source, err := os.Getwd()
	if err != nil {
		return 9
	}
	work := filepath.Join(filepath.Dir(self), "go.work")
	if err := os.WriteFile(work, []byte(fmt.Sprintf("go 1.26.7\nuse (\n%q\n%q\n)\n", source, config.Framework)), 0600); err != nil {
		return 9
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, config.RealGo, os.Args[1:]...)
	command.Env = append(os.Environ(), "GOWORK="+work, "GOPROXY=off", "GOSUMDB=off", "GOMODCACHE="+config.ModuleCache, "GOCACHE="+config.GoCache, "GOFLAGS=-buildvcs=false")
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.WaitDelay = time.Second
	err = command.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return 9
	}
	return 0
}
