package fakes

import (
	"bytes"
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
	if err := os.WriteFile(work, []byte(fmt.Sprintf("go 1.26.7\nuse (\n%q\n%q\n)\n\nreplace github.com/defos-ai/commander/framework v0.3.0 => %q\n", source, config.Framework, config.Framework)), 0600); err != nil {
		return 9
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, config.RealGo, os.Args[1:]...)
	command.Env = append(os.Environ(), "GOWORK="+work, "GOPROXY=off", "GOSUMDB=off", "GOMODCACHE="+config.ModuleCache, "GOCACHE="+config.GoCache, "GOFLAGS=-buildvcs=false")
	var stdout, stderr goOutput
	command.Stdin = os.Stdin
	command.Stdout, command.Stderr = io.MultiWriter(os.Stdout, &stdout), io.MultiWriter(os.Stderr, &stderr)
	command.WaitDelay = time.Second
	err = command.Run()
	recordGoOutput(self, stdout.Bytes(), stderr.Bytes())
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return 9
	}
	return 0
}

type goOutput struct{ bytes.Buffer }

func (output *goOutput) Write(data []byte) (int, error) {
	count := len(data)
	remaining := 32<<10 - output.Len()
	if len(data) > remaining {
		data = data[:remaining]
	}
	_, err := output.Buffer.Write(data)
	return count, err
}

func recordGoOutput(self string, stdout, stderr []byte) {
	file, err := os.OpenFile(filepath.Join(filepath.Dir(self), "go.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() >= 64<<10 {
		return
	}
	_, _ = io.CopyN(file, io.MultiReader(bytes.NewReader(stdout), bytes.NewReader(stderr), strings.NewReader("\n")), 64<<10-info.Size())
}
