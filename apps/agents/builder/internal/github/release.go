package github

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder/internal/releaseworkflow"
	"github.com/defos-ai/commander/framework/agent/manifest"
	"github.com/defos-ai/commander/framework/id"
)

const releaseWorkflow = ".github/workflows/make-release.yml"

type Workflow struct {
	ID    int64  `json:"id"`
	Path  string `json:"path"`
	State string `json:"state"`
}

type WorkflowRun struct {
	ID           int64  `json:"id"`
	WorkflowID   int64  `json:"workflow_id"`
	HeadSHA      string `json:"head_sha"`
	HeadBranch   string `json:"head_branch"`
	Event        string `json:"event"`
	DisplayTitle string `json:"display_title"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	Path         string `json:"path"`
	RepositoryID int64  `json:"-"`
	Repository   struct {
		ID int64 `json:"id"`
	} `json:"repository"`
}

func repositoryPath(repository string) (string, error) {
	owner, name, err := parseRepository(repository)
	if err != nil {
		return "", err
	}
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

func validCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func validBranch(value string) bool {
	return value != "" && len(value) <= 255 && !strings.ContainsAny(value, "\x00\r\n")
}

func (c Client) GetWorkflow(ctx context.Context, repository string) (Workflow, error) {
	path, err := repositoryPath(repository)
	if err != nil {
		return Workflow{}, err
	}
	var result Workflow
	if err := c.request(ctx, http.MethodGet, path+"/actions/workflows/make-release.yml", nil, &result); err != nil {
		return Workflow{}, err
	}
	if result.ID <= 0 || result.Path != releaseWorkflow || result.State != "active" {
		return Workflow{}, errors.New("GitHub release workflow must be active at .github/workflows/make-release.yml")
	}
	return result, nil
}

// GetCommit reads a branch reference, rather than resolving an ambiguous name
// that GitHub could interpret as a tag or abbreviated commit.
func (c Client) GetCommit(ctx context.Context, repository, branch string) (string, error) {
	path, err := repositoryPath(repository)
	if err != nil {
		return "", err
	}
	if !validBranch(branch) {
		return "", errors.New("release requires a nonempty bounded branch name")
	}
	var result gitReference
	if err := c.request(ctx, http.MethodGet, path+"/git/ref/heads/"+url.PathEscape(branch), nil, &result); err != nil {
		return "", err
	}
	if result.Ref != "refs/heads/"+branch || result.Object.Type != "commit" || !validCommit(result.Object.SHA) {
		return "", errors.New("GitHub returned an unexpected branch reference")
	}
	return result.Object.SHA, nil
}

func (c Client) VerifyWorkflow(ctx context.Context, repository, commit string) error {
	path, err := repositoryPath(repository)
	if err != nil {
		return err
	}
	if !validCommit(commit) {
		return errors.New("release requires an exact lowercase commit SHA")
	}
	var result struct {
		Type     string `json:"type"`
		Path     string `json:"path"`
		Encoding string `json:"encoding"`
		Size     int64  `json:"size"`
		Content  string `json:"content"`
	}
	if err := c.request(ctx, http.MethodGet, path+"/contents/"+releaseWorkflow+"?ref="+commit, nil, &result); err != nil {
		return err
	}
	const maxWorkflowBytes = 64 << 10
	if result.Type != "file" || result.Path != releaseWorkflow || result.Encoding != "base64" || result.Size <= 0 || result.Size > maxWorkflowBytes || len(result.Content) > base64.StdEncoding.EncodedLen(maxWorkflowBytes)+2048 {
		return errors.New("GitHub release workflow content is missing, unsafe or too large")
	}
	data, err := base64.StdEncoding.DecodeString(result.Content)
	if err != nil || len(data) > maxWorkflowBytes || int64(len(data)) != result.Size {
		return errors.New("GitHub returned invalid release workflow content")
	}
	return releaseworkflow.Validate(data)
}

func (c Client) DispatchRelease(ctx context.Context, repository, branch, version, commit, requestID string) (int64, error) {
	path, err := repositoryPath(repository)
	if err != nil {
		return 0, err
	}
	if !validBranch(branch) || !manifest.ValidVersion(version) || !validCommit(commit) || !id.Valid(requestID) {
		return 0, errors.New("release dispatch requires a branch, stable version, exact commit and execution request ID")
	}
	body := struct {
		Ref    string            `json:"ref"`
		Inputs map[string]string `json:"inputs"`
	}{Ref: branch, Inputs: map[string]string{"version": version, "expected_commit": commit, "request_id": requestID}}
	var result struct {
		ID int64 `json:"workflow_run_id"`
	}
	if err := c.request(ctx, http.MethodPost, path+"/actions/workflows/make-release.yml/dispatches", body, &result); err != nil {
		return 0, err
	}
	if result.ID <= 0 {
		return 0, errors.New("GitHub release dispatch returned no valid workflow run ID; inspect the saved request before retrying")
	}
	return result.ID, nil
}

func (c Client) GetWorkflowRun(ctx context.Context, repository string, runID int64) (WorkflowRun, error) {
	path, err := repositoryPath(repository)
	if err != nil {
		return WorkflowRun{}, err
	}
	if runID <= 0 {
		return WorkflowRun{}, errors.New("release workflow run ID must be positive")
	}
	var result WorkflowRun
	if err := c.request(ctx, http.MethodGet, path+"/actions/runs/"+strconv.FormatInt(runID, 10), nil, &result); err != nil {
		return WorkflowRun{}, err
	}
	result.RepositoryID = result.Repository.ID
	if result.ID != runID || result.WorkflowID <= 0 || result.RepositoryID <= 0 || !validCommit(result.HeadSHA) || !validBranch(result.HeadBranch) || result.Path != releaseWorkflow {
		return WorkflowRun{}, errors.New("GitHub returned an unexpected release workflow run")
	}
	return result, nil
}

// FindReleaseRun reconciles a dispatch whose response or checkpoint acknowledgement
// was lost. A missing match is not permission to repeat the dispatch.
func (c Client) FindReleaseRun(ctx context.Context, repository, commit, version, requestID string) (int64, error) {
	path, err := repositoryPath(repository)
	if err != nil {
		return 0, err
	}
	if !validCommit(commit) || !manifest.ValidVersion(version) || !id.Valid(requestID) {
		return 0, errors.New("release reconciliation requires the saved commit, stable version and request ID")
	}
	var matched int64
	for page := 1; page <= 10; page++ {
		query := url.Values{"event": {"workflow_dispatch"}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var result struct {
			TotalCount int           `json:"total_count"`
			Runs       []WorkflowRun `json:"workflow_runs"`
		}
		if err := c.request(ctx, http.MethodGet, path+"/actions/workflows/make-release.yml/runs?"+query.Encode(), nil, &result); err != nil {
			return 0, err
		}
		if result.TotalCount < 0 || result.TotalCount > 1000 || len(result.Runs) > 100 {
			return 0, errors.New("release reconciliation exceeds the bounded workflow history; inspect GitHub before retrying")
		}
		for _, run := range result.Runs {
			if run.Event != "workflow_dispatch" || run.DisplayTitle != "Release agent "+version+" "+requestID {
				continue
			}
			if run.HeadSHA != commit || run.ID <= 0 || run.Path != releaseWorkflow || matched != 0 && matched != run.ID {
				return 0, errors.New("release request matches invalid or multiple workflow runs; inspect GitHub before retrying")
			}
			matched = run.ID
		}
		if len(result.Runs) < 100 {
			return matched, nil
		}
	}
	return matched, nil
}

type gitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type gitReference struct {
	Ref    string    `json:"ref"`
	Object gitObject `json:"object"`
}

func (c Client) verifyReleaseTag(ctx context.Context, path, version, commit string) error {
	var reference gitReference
	if err := c.request(ctx, http.MethodGet, path+"/git/ref/tags/"+version, nil, &reference); err != nil {
		return err
	}
	if reference.Ref != "refs/tags/"+version {
		return errors.New("GitHub returned an unexpected release tag")
	}
	object := reference.Object
	for depth := 0; depth < 8; depth++ {
		if !validCommit(object.SHA) {
			return errors.New("GitHub release tag has an invalid target")
		}
		if object.Type == "commit" {
			if object.SHA != commit {
				return errors.New("release tag does not point to the reviewed source commit")
			}
			return nil
		}
		if object.Type != "tag" {
			return errors.New("GitHub release tag must resolve to a commit")
		}
		var tag struct {
			SHA    string    `json:"sha"`
			Object gitObject `json:"object"`
		}
		if err := c.request(ctx, http.MethodGet, path+"/git/tags/"+object.SHA, nil, &tag); err != nil {
			return err
		}
		if tag.SHA != object.SHA {
			return errors.New("GitHub returned an unexpected annotated tag")
		}
		object = tag.Object
	}
	return errors.New("release tag exceeds the annotated-tag depth limit")
}

type releaseAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

func (c Client) VerifyRelease(ctx context.Context, repository, version, commit, agentType string) error {
	owner, _, err := parseRepository(repository)
	if err != nil {
		return err
	}
	if !manifest.ValidVersion(version) || !validCommit(commit) || !id.ValidType(agentType) {
		return errors.New("release verification requires a stable version, exact commit and valid agent type")
	}
	path, _ := repositoryPath(repository)
	var release struct {
		ID          int64          `json:"id"`
		TagName     string         `json:"tag_name"`
		Draft       *bool          `json:"draft"`
		Prerelease  *bool          `json:"prerelease"`
		PublishedAt *time.Time     `json:"published_at"`
		Assets      []releaseAsset `json:"assets"`
	}
	if err := c.request(ctx, http.MethodGet, path+"/releases/tags/"+version, nil, &release); err != nil {
		return err
	}
	if release.ID <= 0 || release.TagName != version || release.Draft == nil || *release.Draft || release.Prerelease == nil || *release.Prerelease || release.PublishedAt == nil || release.PublishedAt.IsZero() || len(release.Assets) > 100 {
		return errors.New("release must be the requested published non-prerelease version with bounded assets")
	}
	if err := c.verifyReleaseTag(ctx, path, version, commit); err != nil {
		return err
	}
	assets := make(map[string]releaseAsset, len(release.Assets))
	seenIDs := make(map[int64]bool, len(release.Assets))
	for _, asset := range release.Assets {
		if _, exists := assets[asset.Name]; exists || asset.ID <= 0 || seenIDs[asset.ID] || asset.Name == "" || asset.State != "uploaded" || asset.Size <= 0 {
			return errors.New("GitHub release contains invalid or duplicate uploaded assets")
		}
		assets[asset.Name] = asset
		seenIDs[asset.ID] = true
	}
	metadata, exists := assets["agent.yaml"]
	if !exists || metadata.Size > manifest.MaxBytes {
		return errors.New("release is missing a bounded agent.yaml asset")
	}
	data, err := c.downloadManifest(ctx, path, metadata)
	if err != nil {
		return err
	}
	parsed, err := manifest.Parse(data, version)
	if err != nil {
		return errors.New("published agent.yaml is invalid for the requested release version")
	}
	if parsed.Type != agentType || len(parsed.Native) != 6 || parsed.Docker == nil || !strings.HasPrefix(parsed.Docker.Image, "ghcr.io/"+strings.ToLower(owner)+"/commander-"+agentType+"@sha256:") {
		return errors.New("published manifest must match the agent identity, six native platforms and repository owner's Docker image")
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "commander-" + agentType + "_" + goos + "_" + arch
			if goos == "windows" {
				name += ".exe"
			}
			entry, found := parsed.Native[goos+"/"+arch]
			asset, uploaded := assets[name]
			if !found || entry.Asset != name || !uploaded || asset.Digest != "sha256:"+entry.SHA256 {
				return errors.New("published native assets must match all manifest names and SHA-256 checksums")
			}
		}
	}
	return nil
}

func (c Client) downloadManifest(ctx context.Context, path string, asset releaseAsset) ([]byte, error) {
	request, err := c.newRequest(ctx, http.MethodGet, path+"/releases/assets/"+strconv.FormatInt(asset.ID, 10), nil, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 30 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many release asset redirects")
		}
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer"} {
			request.Header.Del(name)
		}
		if request.URL.Scheme != "https" || request.URL.User != nil || request.URL.Opaque != "" || request.URL.Fragment != "" || request.URL.Port() != "" && request.URL.Port() != "443" {
			return errors.New("release asset redirects require HTTPS GitHub download hosts")
		}
		switch request.URL.Hostname() {
		case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		default:
			return errors.New("release asset redirect must stay on GitHub download hosts")
		}
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, requestError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub release asset request failed (HTTP %d)", response.StatusCode)
	}
	if response.ContentLength > manifest.MaxBytes {
		return nil, errors.New("published agent.yaml exceeds the manifest size limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, manifest.MaxBytes+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(data) > manifest.MaxBytes || int64(len(data)) != asset.Size {
		return nil, errors.New("cannot read the complete bounded agent.yaml asset")
	}
	digest := sha256.Sum256(data)
	if asset.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return nil, errors.New("published agent.yaml does not match its GitHub asset SHA-256 digest")
	}
	return data, nil
}
