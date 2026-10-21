package builder_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/agent/manifest"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
)

const releaseCommit = "1234567890abcdef1234567890abcdef12345678"
const releaseVersion = "v0.3.0"
const releaseAgentType = "built-agent"
const releaseRunID int64 = 93

type releaseHTTPFixture struct {
	t                    *testing.T
	commandID            string
	requests, dispatches int
	status, conclusion   string
	headSHA, branchSHA   string
	tagSHA, titleVersion string
	private, draft       bool
	redirectManifest     bool
	cdnRequests          int
	lostDispatchAck      bool
	hideRun              bool
	workflow             string
	assets               []map[string]any
	manifest             []byte
	requestID            string
	onRunRead            func()
}

func (f *releaseHTTPFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	f.t.Helper()
	f.requests++
	if f.redirectManifest && request.URL.Host == "release-assets.githubusercontent.com" {
		f.cdnRequests++
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Path != "/fixture/agent.yaml" {
			f.t.Error("unexpected GitHub manifest CDN request")
		}
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer"} {
			if request.Header.Get(name) != "" {
				f.t.Errorf("manifest CDN request received %s", name)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(f.manifest)), Request: request}, nil
	}
	if request.URL.Scheme != "https" || request.URL.Host != "api.github.com" || request.Header.Get("Authorization") != "Bearer "+publicationToken || request.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
		f.t.Error("release request changed the fixed GitHub origin or managed authentication")
		return nil, errors.New("unexpected GitHub origin or authorization")
	}
	status, body := http.StatusOK, any(nil)
	switch request.Method + " " + request.URL.Path {
	case "GET /repos/" + publicationRepository:
		body = f.repository()
	case "GET /repos/" + publicationRepository + "/actions/workflows/make-release.yml":
		body = map[string]any{"id": 72, "name": "Make release", "path": ".github/workflows/make-release.yml", "state": "active"}
	case "GET /repos/" + publicationRepository + "/git/ref/heads/main":
		body = map[string]any{"ref": "refs/heads/main", "object": map[string]any{"type": "commit", "sha": f.branchSHA}}
	case "GET /repos/" + publicationRepository + "/contents/.github/workflows/make-release.yml":
		if request.URL.Query().Get("ref") != releaseCommit {
			f.t.Error("release did not inspect the workflow at the requested commit")
		}
		body = map[string]any{"type": "file", "path": ".github/workflows/make-release.yml", "size": len(f.workflow), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(f.workflow))}
	case "POST /repos/" + publicationRepository + "/actions/workflows/make-release.yml/dispatches":
		var input struct {
			Ref    string            `json:"ref"`
			Inputs map[string]string `json:"inputs"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Ref != "main" || input.Inputs["version"] != releaseVersion || input.Inputs["expected_commit"] != releaseCommit || input.Inputs["request_id"] != f.commandID || len(input.Inputs) != 3 {
			f.t.Errorf("release dispatch must target the source commit and execution: %+v %v", input, err)
			return nil, errors.New("unexpected workflow dispatch")
		}
		f.dispatches++
		f.requestID = input.Inputs["request_id"]
		if f.lostDispatchAck {
			return nil, errors.New("dispatch response lost: " + publicationToken)
		}
		body = map[string]any{"workflow_run_id": releaseRunID, "run_url": "https://untrusted.invalid/run", "html_url": "https://untrusted.invalid/html"}
	case fmt.Sprintf("GET /repos/%s/actions/runs/%d", publicationRepository, releaseRunID):
		body = f.run()
		if f.onRunRead != nil {
			f.onRunRead()
		}
	case "GET /repos/" + publicationRepository + "/actions/workflows/make-release.yml/runs":
		if request.URL.Query().Get("event") != "workflow_dispatch" {
			f.t.Error("uncertain dispatch recovery must restrict the workflow event")
		}
		var runs []any
		if f.requestID != "" && !f.hideRun {
			runs = []any{f.run()}
		}
		body = map[string]any{"total_count": len(runs), "workflow_runs": runs}
	case "GET /repos/" + publicationRepository + "/releases/tags/" + releaseVersion:
		body = map[string]any{"id": 61, "tag_name": releaseVersion, "draft": f.draft, "prerelease": false, "published_at": "2026-10-21T12:00:00Z", "assets": f.assets, "html_url": "https://untrusted.invalid/release"}
	case "GET /repos/" + publicationRepository + "/git/ref/tags/" + releaseVersion:
		body = map[string]any{"ref": "refs/tags/" + releaseVersion, "object": map[string]any{"type": "commit", "sha": f.tagSHA}}
	case "GET /repos/" + publicationRepository + "/releases/assets/100":
		if request.Header.Get("Accept") != "application/octet-stream" {
			f.t.Error("release manifest must use the fixed release asset endpoint")
		}
		if f.redirectManifest {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://release-assets.githubusercontent.com/fixture/agent.yaml"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(f.manifest)), Request: request}, nil
	default:
		f.t.Errorf("unexpected release request: %s %s", request.Method, request.URL.Path)
		return nil, errors.New("unexpected GitHub release request")
	}
	data, _ := json.Marshal(body)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
}

func (f *releaseHTTPFixture) repository() map[string]any {
	return map[string]any{"id": 42, "full_name": publicationRepository, "private": f.private, "default_branch": "main"}
}

func (f *releaseHTTPFixture) run() map[string]any {
	requestID := f.requestID
	if requestID == "" {
		requestID = f.commandID
	}
	return map[string]any{"id": releaseRunID, "workflow_id": 72, "event": "workflow_dispatch", "path": ".github/workflows/make-release.yml", "head_branch": "main", "head_sha": f.headSHA, "display_title": "Release agent " + f.titleVersion + " " + requestID, "status": f.status, "conclusion": f.conclusion, "repository": f.repository(), "html_url": "https://untrusted.invalid/run"}
}

func (f *releaseHTTPFixture) packageAssets(t *testing.T) {
	t.Helper()
	value := manifest.Manifest{SchemaVersion: 1, Type: releaseAgentType, Version: releaseVersion, Native: map[string]manifest.NativeAsset{}, Docker: &manifest.Docker{Image: "ghcr.io/fixture/commander-" + releaseAgentType + "@sha256:" + strings.Repeat("a", 64)}}
	f.assets = nil
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			platform := goos + "/" + goarch
			name := "commander-" + releaseAgentType + "_" + goos + "_" + goarch
			if goos == "windows" {
				name += ".exe"
			}
			content := []byte("native executable for " + platform)
			digest := sha256.Sum256(content)
			hexDigest := hex.EncodeToString(digest[:])
			value.Native[platform] = manifest.NativeAsset{Asset: name, SHA256: hexDigest}
			f.assets = append(f.assets, map[string]any{"id": 101 + len(f.assets), "name": name, "state": "uploaded", "size": len(content), "digest": "sha256:" + hexDigest})
		}
	}
	var err error
	f.manifest, err = manifest.Render(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(f.manifest)
	f.assets = append(f.assets, map[string]any{"id": 100, "name": "agent.yaml", "state": "uploaded", "size": len(f.manifest), "digest": "sha256:" + hex.EncodeToString(digest[:])})
}

type releaseFixture struct {
	action      *builderactions.ReleaseAgent
	input       builderactions.ReleaseAgentInput
	ctx         context.Context
	checkpoints *publicationCheckpoints
	http        *releaseHTTPFixture
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	executor, err := builderactions.NewReleaseAgent()
	if err != nil {
		t.Fatal(err)
	}
	status, err := executor.Discover(t.Context(), &environment.Local{}, &environment.DiscoveryScope{})
	if err != nil || status != action.Ready {
		t.Fatalf("release discovery should need no external executable: %s %v", status, err)
	}
	if names := executor.Action().RequiredSecrets; len(names) != 1 || names[0] != "GITHUB_TOKEN" {
		t.Fatal("release must declare managed GITHUB_TOKEN")
	}
	command, err := messages.NewMessage(messages.RunAction, "builder-agent-12345678", actionmodel.ActionCall{ActionName: builderactions.ReleaseAgentName, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	journal := &publicationCheckpoints{record: executions.Execution{Command: command, StartedAt: command.CreatedAt}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	ctx = action.WithExecution(ctx, action.Execution{CommandID: command.ID})
	ctx = action.WithSecrets(action.WithCheckpoints(ctx, journal), publicationSecrets(publicationToken))
	httpFixture := &releaseHTTPFixture{t: t, commandID: command.ID, status: "queued", headSHA: releaseCommit, branchSHA: releaseCommit, tagSHA: releaseCommit, titleVersion: releaseVersion, workflow: buildReleaseWorkflowFixture}
	httpFixture.packageAssets(t)
	previous := http.DefaultTransport
	http.DefaultTransport = httpFixture
	t.Cleanup(func() { http.DefaultTransport = previous })
	return &releaseFixture{action: executor, input: builderactions.ReleaseAgentInput{Repository: publicationRepository, CommitSHA: releaseCommit, Version: releaseVersion, AgentType: releaseAgentType}, ctx: ctx, checkpoints: journal, http: httpFixture}
}

func (f *releaseFixture) execute(t *testing.T) (action.Result, builderactions.ReleaseAgentOutput, error) {
	t.Helper()
	data, err := json.Marshal(f.input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.action.Run(f.ctx, data)
	var output builderactions.ReleaseAgentOutput
	if len(result.Output) != 0 {
		if decodeErr := json.Unmarshal(result.Output, &output); decodeErr != nil {
			t.Fatal(decodeErr)
		}
	}
	return result, output, err
}
