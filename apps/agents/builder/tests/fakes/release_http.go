package fakes

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
)

type ReleaseState struct {
	Status, Conclusion       string
	HeadSHA, BranchSHA       string
	TagSHA, TitleVersion     string
	Private, Draft           bool
	RedirectManifest         bool
	LostDispatchAck, HideRun bool
	Workflow                 string
	Assets                   []map[string]any
	Manifest                 []byte
	RequestID                string
	OnRunRead                func()
}

type ReleaseConfig struct {
	Token, Repository, Commit, Version, AgentType, Workflow string
	RunID                                                   int64
}

type ReleaseHTTP struct {
	config                            ReleaseConfig
	t                                 *testing.T
	commandID                         string
	state                             ReleaseState
	requests, dispatches, cdnRequests int
}

func NewReleaseHTTP(t *testing.T, commandID string, config ReleaseConfig) *ReleaseHTTP {
	f := &ReleaseHTTP{t: t, commandID: commandID, config: config, state: ReleaseState{
		Status: "queued", HeadSHA: config.Commit, BranchSHA: config.Commit,
		TagSHA: config.Commit, TitleVersion: config.Version, Workflow: config.Workflow,
	}}
	return f
}

func cloneReleaseState(state ReleaseState) ReleaseState {
	state.Manifest = bytes.Clone(state.Manifest)
	if state.Assets != nil {
		assets := make([]map[string]any, len(state.Assets))
		for i, asset := range state.Assets {
			assets[i] = maps.Clone(asset)
		}
		state.Assets = assets
	}
	return state
}

func (f *ReleaseHTTP) Snapshot() ReleaseState { return cloneReleaseState(f.state) }
func (f *ReleaseHTTP) Change(change func(*ReleaseState)) {
	next := cloneReleaseState(f.state)
	change(&next)
	f.state = cloneReleaseState(next)
}
func (f *ReleaseHTTP) RequestCount() int    { return f.requests }
func (f *ReleaseHTTP) DispatchCount() int   { return f.dispatches }
func (f *ReleaseHTTP) CDNRequestCount() int { return f.cdnRequests }

func (f *ReleaseHTTP) RoundTrip(request *http.Request) (*http.Response, error) {
	f.t.Helper()
	f.requests++
	if f.state.RedirectManifest && request.URL.Host == "release-assets.githubusercontent.com" {
		f.cdnRequests++
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Path != "/fixture/agent.yaml" {
			f.t.Error("unexpected GitHub manifest CDN request")
		}
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer"} {
			if request.Header.Get(name) != "" {
				f.t.Errorf("manifest CDN request received %s", name)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(f.state.Manifest)), Request: request}, nil
	}
	if request.URL.Scheme != "https" || request.URL.Host != "api.github.com" || request.Header.Get("Authorization") != "Bearer "+f.config.Token || request.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
		f.t.Error("release request changed the fixed GitHub origin or managed authentication")
		return nil, errors.New("unexpected GitHub origin or authorization")
	}
	status, body := http.StatusOK, any(nil)
	switch request.Method + " " + request.URL.Path {
	case "GET /repos/" + f.config.Repository:
		body = f.repository()
	case "GET /repos/" + f.config.Repository + "/actions/workflows/make-release.yml":
		body = map[string]any{"id": 72, "name": "Make release", "path": ".github/workflows/make-release.yml", "state": "active"}
	case "GET /repos/" + f.config.Repository + "/git/ref/heads/main":
		body = map[string]any{"ref": "refs/heads/main", "object": map[string]any{"type": "commit", "sha": f.state.BranchSHA}}
	case "GET /repos/" + f.config.Repository + "/contents/.github/workflows/make-release.yml":
		if request.URL.Query().Get("ref") != f.config.Commit {
			f.t.Error("release did not inspect the workflow at the requested commit")
		}
		body = map[string]any{"type": "file", "path": ".github/workflows/make-release.yml", "size": len(f.state.Workflow), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(f.state.Workflow))}
	case "POST /repos/" + f.config.Repository + "/actions/workflows/make-release.yml/dispatches":
		var input struct {
			Ref    string            `json:"ref"`
			Inputs map[string]string `json:"inputs"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Ref != "main" || input.Inputs["version"] != f.config.Version || input.Inputs["expected_commit"] != f.config.Commit || input.Inputs["request_id"] != f.commandID || len(input.Inputs) != 3 {
			f.t.Errorf("release dispatch must target the source commit and execution: %+v %v", input, err)
			return nil, errors.New("unexpected workflow dispatch")
		}
		f.dispatches++
		f.state.RequestID = input.Inputs["request_id"]
		if f.state.LostDispatchAck {
			return nil, errors.New("dispatch response lost: " + f.config.Token)
		}
		body = map[string]any{"workflow_run_id": f.config.RunID, "run_url": "https://untrusted.invalid/run", "html_url": "https://untrusted.invalid/html"}
	case fmt.Sprintf("GET /repos/%s/actions/runs/%d", f.config.Repository, f.config.RunID):
		body = f.run()
		if f.state.OnRunRead != nil {
			f.state.OnRunRead()
		}
	case "GET /repos/" + f.config.Repository + "/actions/workflows/make-release.yml/runs":
		if request.URL.Query().Get("event") != "workflow_dispatch" {
			f.t.Error("uncertain dispatch recovery must restrict the workflow event")
		}
		var runs []any
		if f.state.RequestID != "" && !f.state.HideRun {
			runs = []any{f.run()}
		}
		body = map[string]any{"total_count": len(runs), "workflow_runs": runs}
	case "GET /repos/" + f.config.Repository + "/releases/tags/" + f.config.Version:
		body = map[string]any{"id": 61, "tag_name": f.config.Version, "draft": f.state.Draft, "prerelease": false, "published_at": "2026-10-21T12:00:00Z", "assets": f.state.Assets, "html_url": "https://untrusted.invalid/release"}
	case "GET /repos/" + f.config.Repository + "/git/ref/tags/" + f.config.Version:
		body = map[string]any{"ref": "refs/tags/" + f.config.Version, "object": map[string]any{"type": "commit", "sha": f.state.TagSHA}}
	case "GET /repos/" + f.config.Repository + "/releases/assets/100":
		if request.Header.Get("Accept") != "application/octet-stream" {
			f.t.Error("release manifest must use the fixed release asset endpoint")
		}
		if f.state.RedirectManifest {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://release-assets.githubusercontent.com/fixture/agent.yaml"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(f.state.Manifest)), Request: request}, nil
	default:
		f.t.Errorf("unexpected release request: %s %s", request.Method, request.URL.Path)
		return nil, errors.New("unexpected GitHub release request")
	}
	data, _ := json.Marshal(body)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
}

func (f *ReleaseHTTP) repository() map[string]any {
	return map[string]any{"id": 42, "full_name": f.config.Repository, "private": f.state.Private, "default_branch": "main"}
}

func (f *ReleaseHTTP) run() map[string]any {
	requestID := f.state.RequestID
	if requestID == "" {
		requestID = f.commandID
	}
	return map[string]any{"id": f.config.RunID, "workflow_id": 72, "event": "workflow_dispatch", "path": ".github/workflows/make-release.yml", "head_branch": "main", "head_sha": f.state.HeadSHA, "display_title": "Release agent " + f.state.TitleVersion + " " + requestID, "status": f.state.Status, "conclusion": f.state.Conclusion, "repository": f.repository(), "html_url": "https://untrusted.invalid/run"}
}
