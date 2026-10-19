package packages

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
)

const maxGitHubMetadataBytes = 2 << 20

type githubRelease struct {
	TagName     string     `json:"tag_name"`
	Draft       *bool      `json:"draft"`
	Prerelease  *bool      `json:"prerelease"`
	PublishedAt *time.Time `json:"published_at"`
	Assets      []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"assets"`
}

func (r *Resolver) getAsset(ctx context.Context, ref agentpackages.Reference, name string) (*http.Response, error) {
	if len(GitHubToken(ctx)) == 0 {
		return r.get(ctx, assetURL(ref, name))
	}
	base := "https://api.github.com/repos/" + ref.Owner + "/" + ref.Repository + "/releases/"
	release, err := r.readRelease(ctx, base+"tags/"+url.PathEscape(ref.ReleaseTag()))
	if err != nil {
		return nil, err
	}
	if release.TagName != ref.ReleaseTag() || release.Draft == nil || *release.Draft || release.Prerelease == nil || *release.Prerelease || release.PublishedAt == nil || release.PublishedAt.IsZero() {
		return nil, errors.New("GitHub release must match the requested published stable tag")
	}
	var assetID int64
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		if assetID != 0 || asset.ID <= 0 {
			return nil, errors.New("GitHub release has invalid or duplicate package assets")
		}
		assetID = asset.ID
	}
	if assetID == 0 {
		return nil, errors.New("GitHub release is missing the requested package asset")
	}
	// Ignore API-provided asset URLs: credentials go only to this constructed
	// api.github.com endpoint, and redirects are followed without credentials.
	return r.getGitHub(ctx, base+"assets/"+strconv.FormatInt(assetID, 10), "application/octet-stream")
}

func (r *Resolver) readRelease(ctx context.Context, target string) (githubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := r.getGitHub(ctx, target, "application/vnd.github+json")
	if err != nil {
		return githubRelease{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxGitHubMetadataBytes+1))
	if err != nil {
		return githubRelease{}, downloadError(ctx, "read GitHub release metadata")
	}
	if err := ctx.Err(); err != nil {
		return githubRelease{}, err
	}
	if len(data) > maxGitHubMetadataBytes {
		return githubRelease{}, errors.New("GitHub release metadata exceeds the 2 MiB limit")
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return githubRelease{}, errors.New("GitHub returned invalid release metadata")
	}
	return release, nil
}

func (r *Resolver) getGitHub(ctx context.Context, target, accept string) (*http.Response, error) {
	token := GitHubToken(ctx)
	if len(token) == 0 {
		return nil, errors.New("GITHUB_TOKEN is required for private GitHub packages")
	}
	for _, value := range token {
		if value <= ' ' || value >= 0x7f {
			return nil, errors.New("GITHUB_TOKEN must not contain whitespace or control characters")
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil || request.URL.Scheme != "https" || request.URL.Host != "api.github.com" || request.URL.User != nil || request.URL.RawQuery != "" || request.URL.Fragment != "" {
		return nil, errors.New("invalid GitHub API request URL")
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set("Accept", accept)
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	return r.do(request)
}
