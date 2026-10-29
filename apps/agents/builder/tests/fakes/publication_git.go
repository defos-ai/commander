package fakes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type PublicationGit struct {
	Token, Repository          string
	Bare, Calls, Started, Mode string
}

type PublicationGitCall struct {
	Command, Directory, Home string
}

// Git still creates and transfers real commits. Only the fixed GitHub remote is
// replaced, inside the discovered fixture executable, by a local bare repository.
func executePublicationGit(realGit string, config PublicationGit) error {
	args := append([]string(nil), os.Args[1:]...)
	index := 0
	for index+1 < len(args) && args[index] == "-c" {
		index += 2
	}
	if index >= len(args) || os.Getenv("GIT_ALLOW_PROTOCOL") != "https" {
		return errors.New("publication did not restrict Git protocols")
	}
	name := args[index]
	for _, key := range []string{"GITHUB_TOKEN", "OPENAI_API_KEY", "CODEX_API_KEY", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD"} {
		if _, found := os.LookupEnv(key); found {
			return fmt.Errorf("publication inherited host variable %s", key)
		}
	}
	for _, value := range args {
		if strings.Contains(value, config.Token) {
			return errors.New("Git argument contains publication token")
		}
	}
	remote := name == "ls-remote" || name == "push"
	if remote {
		want := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+config.Token))
		if os.Getenv("GIT_CONFIG_COUNT") != "1" || os.Getenv("GIT_CONFIG_KEY_0") != "http.https://github.com/.extraHeader" || os.Getenv("GIT_CONFIG_VALUE_0") != want {
			return errors.New("remote Git did not receive scoped managed authentication")
		}
		found := false
		for i, value := range args[index+1:] {
			if value == "https://github.com/"+config.Repository+".git" {
				args[index+1+i] = config.Bare
				found = true
			}
			if name == "push" && (strings.HasPrefix(value, "+") || strings.HasPrefix(value, "--force") || value == "-f") {
				return errors.New("publication attempted a force push")
			}
		}
		if !found {
			return errors.New("Git did not use the fixed repository origin")
		}
	} else if os.Getenv("GIT_CONFIG_VALUE_0") != "" {
		return errors.New("local snapshot command received remote authentication")
	}
	switch name {
	case "init", "add", "commit", "rev-parse", "ls-remote", "push":
	default:
		return errors.New("unexpected publication Git command")
	}
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	ceiling, ceilingErr := os.Stat(os.Getenv("GIT_CEILING_DIRECTORIES"))
	current, currentErr := os.Stat(directory)
	if ceilingErr != nil || currentErr != nil || !os.SameFile(ceiling, current) {
		return errors.New("Git did not isolate discovery from parent repositories")
	}
	call, _ := json.Marshal(PublicationGitCall{Command: name, Directory: directory, Home: os.Getenv("HOME")})
	log, err := os.OpenFile(config.Calls, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = log.Write(append(call, '\n'))
	closeErr := log.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if name == "ls-remote" && config.Mode == "wait" {
		if err := os.WriteFile(config.Started, []byte("started"), 0600); err != nil {
			return err
		}
		time.Sleep(30 * time.Second)
		return errors.New("blocking publication Git was not cancelled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, realGit, args...)
	command.Env = append(os.Environ(), "GIT_ALLOW_PROTOCOL=file")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return err
	}
	if data, err := os.ReadFile(filepath.Join(directory, ".git", "config")); err == nil && (bytes.Contains(data, []byte(config.Token)) || bytes.Contains(data, []byte("extraHeader"))) {
		return errors.New("publication persisted Git authentication")
	}
	if name == "push" && config.Mode == "lost-push-ack" {
		return errors.New("untrusted diagnostic: " + config.Token)
	}
	return nil
}
