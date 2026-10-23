package builder_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
)

const publicationToken = "github-managed-publication-test-token"
const publicationRepository = "fixture/built-agent"

type publicationGitFixture struct {
	Bare, Calls, Started, Mode string
}

type publicationGitCall struct {
	Command, Directory, Home string
}

// Git still creates and transfers real commits. Only the fixed GitHub remote is
// replaced, inside the discovered fixture executable, by a local bare repository.
func executePublicationGitFixture(realGit string, config publicationGitFixture) error {
	args := append([]string(nil), os.Args[1:]...)
	index := 0
	for index+1 < len(args) && args[index] == "-c" {
		index += 2
	}
	if index >= len(args) || os.Getenv("GIT_ALLOW_PROTOCOL") != "https" {
		return errors.New("publication did not restrict Git protocols")
	}
	name := args[index]
	for _, key := range []string{"GITHUB_TOKEN", "CODEX_API_KEY", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD"} {
		if _, found := os.LookupEnv(key); found {
			return fmt.Errorf("publication inherited host variable %s", key)
		}
	}
	for _, value := range args {
		if strings.Contains(value, publicationToken) {
			return errors.New("Git argument contains publication token")
		}
	}
	remote := name == "ls-remote" || name == "push"
	if remote {
		want := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+publicationToken))
		if os.Getenv("GIT_CONFIG_COUNT") != "1" || os.Getenv("GIT_CONFIG_KEY_0") != "http.https://github.com/.extraHeader" || os.Getenv("GIT_CONFIG_VALUE_0") != want {
			return errors.New("remote Git did not receive scoped managed authentication")
		}
		found := false
		for i, value := range args[index+1:] {
			if value == "https://github.com/"+publicationRepository+".git" {
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
	call, _ := json.Marshal(publicationGitCall{Command: name, Directory: directory, Home: os.Getenv("HOME")})
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
	if data, err := os.ReadFile(filepath.Join(directory, ".git", "config")); err == nil && (bytes.Contains(data, []byte(publicationToken)) || bytes.Contains(data, []byte("extraHeader"))) {
		return errors.New("publication persisted Git authentication")
	}
	if name == "push" && config.Mode == "lost-push-ack" {
		return errors.New("untrusted diagnostic: " + publicationToken)
	}
	return nil
}

type publicationHTTPFixture struct {
	mu                sync.Mutex
	exists, private   bool
	id                int64
	requests, creates int
}

func (f *publicationHTTPFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if request.URL.Scheme != "https" || request.URL.Host != "api.github.com" || request.Header.Get("Authorization") != "Bearer "+publicationToken {
		return nil, errors.New("unexpected GitHub origin or authorization")
	}
	status, body := http.StatusOK, any(nil)
	switch request.Method + " " + request.URL.Path {
	case "GET /repos/" + publicationRepository:
		if !f.exists {
			status, body = http.StatusNotFound, map[string]any{"message": "Not found"}
		} else {
			body = f.repository()
		}
	case "GET /user":
		body = map[string]any{"login": "fixture"}
	case "POST /user/repos":
		var input struct {
			Name     string `json:"name"`
			Private  bool   `json:"private"`
			AutoInit bool   `json:"auto_init"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Name != "built-agent" || input.AutoInit || f.exists {
			return nil, errors.New("unexpected repository creation")
		}
		f.exists, f.private = true, input.Private
		f.creates++
		status, body = http.StatusCreated, f.repository()
	default:
		return nil, errors.New("unexpected GitHub request; visibility must never be changed")
	}
	data, _ := json.Marshal(body)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
}

func (f *publicationHTTPFixture) repository() map[string]any {
	return map[string]any{"id": f.id, "full_name": publicationRepository, "private": f.private, "default_branch": "main"}
}

type publicationCheckpoints struct {
	mu        sync.Mutex
	record    executions.Execution
	afterSave func(string) error
}

func (p *publicationCheckpoints) LoadCheckpoint(ctx context.Context, name string) (json.RawMessage, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, found := p.record.Checkpoints[name]
	return bytes.Clone(data), found, ctx.Err()
}

func (p *publicationCheckpoints) SaveCheckpoint(ctx context.Context, name string, data json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	_, err := p.record.RecordCheckpoint(name, data)
	p.mu.Unlock()
	if err == nil && p.afterSave != nil {
		return p.afterSave(name)
	}
	return err
}

type publicationSecrets string

func (s publicationSecrets) Secret(name string) ([]byte, error) {
	if name != "GITHUB_TOKEN" {
		return nil, action.ErrSecretUnavailable
	}
	return []byte(s), nil
}

type publicationFixture struct {
	action      *builderactions.PublishAgent
	files       artifacts.ArtifactFileStore
	input       builderactions.PublishAgentInput
	ctx         context.Context
	checkpoints *publicationCheckpoints
	http        *publicationHTTPFixture
	git         publicationGitFixture
}

func newPublicationFixture(t *testing.T, visibility, mode string) *publicationFixture {
	t.Helper()
	files, err := fsartifacts.NewArtifactFileFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	control := t.TempDir()
	git := publicationGitFixture{Bare: filepath.Join(control, "remote.git"), Calls: filepath.Join(control, "calls"), Started: filepath.Join(control, "started"), Mode: mode}
	fixtureGit(t, "init", "--bare", "--initial-branch=main", git.Bare)
	scope := gitFixtureScope(t, gitFixtureConfig{Publish: &git})
	executor, err := builderactions.NewPublishAgent(files, visibility)
	if err != nil {
		t.Fatal(err)
	}
	status, err := executor.Discover(t.Context(), &environment.Local{}, scope)
	if err != nil || status != action.Ready {
		t.Fatalf("publication discovery: %s %v", status, err)
	}
	if names := executor.Action().RequiredSecrets; len(names) != 1 || names[0] != "GITHUB_TOKEN" {
		t.Fatal("publication must declare managed GITHUB_TOKEN")
	}
	input := builderactions.PublishAgentInput{Repository: publicationRepository, SourceArtifactID: id.ArtifactID(), Visibility: "public"}
	if visibility != "" {
		input.Visibility = ""
	}
	command, err := messages.NewMessage(messages.RunAction, "builder-agent-12345678", actionmodel.ActionCall{ActionName: builderactions.PublishAgentName, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	journal := &publicationCheckpoints{record: executions.Execution{Command: command, StartedAt: command.CreatedAt}}
	ctx := action.WithExecution(t.Context(), action.Execution{CommandID: command.ID})
	ctx = action.WithSecrets(action.WithCheckpoints(ctx, journal), publicationSecrets(publicationToken))
	httpFixture := &publicationHTTPFixture{id: 42}
	previous := http.DefaultTransport
	http.DefaultTransport = httpFixture
	t.Cleanup(func() { http.DefaultTransport = previous })
	f := &publicationFixture{action: executor, files: files, input: input, ctx: ctx, checkpoints: journal, http: httpFixture, git: git}
	f.source(t, []publicationArchiveEntry{{name: "README.md", text: "# Built agent\n"}, {name: "cmd/main.go", text: "package main\nfunc main() {}\n"}})
	return f
}

type publicationArchiveEntry struct {
	name, text string
	kind       byte
}

func (f *publicationFixture) source(t *testing.T, entries []publicationArchiveEntry) {
	t.Helper()
	var data bytes.Buffer
	compressed := gzip.NewWriter(&data)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0600, Typeflag: kind, Size: int64(len(entry.text))}
		if kind == tar.TypeSymlink {
			header.Linkname = "README.md"
			header.Size = 0
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := io.WriteString(archive, entry.text); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := f.files.Put(t.Context(), f.input.SourceArtifactID, artifacts.FileSource{Name: "source.tar.gz", MediaType: "application/gzip", Data: data.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	f.input.SourceFile = file
}

func (f *publicationFixture) run(t *testing.T) (action.Result, error) {
	t.Helper()
	data, err := json.Marshal(f.input)
	if err != nil {
		t.Fatal(err)
	}
	return f.action.Run(f.ctx, data)
}

func (f *publicationFixture) calls(t *testing.T, command string) int {
	t.Helper()
	data, err := os.ReadFile(f.git.Calls)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var call publicationGitCall
		if err := json.Unmarshal(line, &call); err != nil {
			t.Fatal(err)
		}
		if call.Command == command {
			n++
		}
		if _, err := os.Stat(call.Home); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("publication retained temporary Git home")
		}
	}
	return n
}
