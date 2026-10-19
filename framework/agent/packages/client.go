package packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/defos-ai/commander/framework/agent/manifest"
)

const maxReleaseBytes = 1 << 20

// Client reads public package metadata. It does not download or launch agents.
type Client struct {
	client *http.Client
}

// NewClient uses no environment proxies, cookies or deployment credentials.
// A supplied transport is trusted host configuration and must not add credentials.
func NewClient(transport http.RoundTripper) *Client {
	if transport == nil {
		transport = &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		}
	}
	return &Client{client: &http.Client{
		Transport: transport,
		// Metadata sources are explicit. A redirect must not silently change a
		// trusted catalog or select a different repository after a rename.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *Client) CloseIdleConnections() { c.client.CloseIdleConnections() }

// LoadCatalog reads a host-configured HTTPS source. Do not pass model/user URLs
// here: selecting the catalog is deployment policy, not installation input.
func (c *Client) LoadCatalog(ctx context.Context, source string) (Catalog, error) {
	target, err := url.Parse(source)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Opaque != "" || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" {
		return Catalog{}, errors.New("catalog source must be an HTTPS URL without credentials, query or fragment")
	}
	data, err := c.read(ctx, source, "agent catalog", MaxCatalogBytes)
	if err != nil {
		return Catalog{}, err
	}
	catalog, err := ParseCatalog(data)
	if err != nil {
		return Catalog{}, errors.New("invalid agent catalog")
	}
	return catalog, nil
}

// Resolve pins a selector to a release version. Exact references need no network.
// Save the returned reference before dispatch; a retry must reuse that value
// rather than resolve a moving channel again. Manifest validation follows in the
// spawner before downloading an executable or launching an image.
func (c *Client) Resolve(ctx context.Context, selector string) (Reference, error) {
	ref, err := ParseSelector(selector)
	if err != nil {
		return Reference{}, err
	}
	if err := ctx.Err(); err != nil {
		return Reference{}, err
	}
	if ref.Version != "stable" {
		return ref, nil
	}
	target := "https://api.github.com/repos/" + ref.Owner + "/" + ref.Repository + "/releases/latest"
	data, err := c.read(ctx, target, "stable release", maxReleaseBytes)
	if err != nil {
		return Reference{}, err
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      *bool  `json:"draft"`
		Prerelease *bool  `json:"prerelease"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return Reference{}, errors.New("invalid stable release metadata")
	}
	if !manifest.ValidVersion(release.Tag) || release.Draft == nil || *release.Draft || release.Prerelease == nil || *release.Prerelease {
		return Reference{}, errors.New("stable release must be a published, non-prerelease vMAJOR.MINOR.PATCH root tag")
	}
	// A module package uses the repository's promoted root version. This requires
	// lockstep releases, as used by Commander's built-in agents.
	ref.Version = release.Tag
	return ref, nil
}

func (c *Client) read(ctx context.Context, target, resource string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid %s URL", resource)
	}
	request.Header.Set("User-Agent", "Commander-Package-Catalog")
	if request.URL.Hostname() == "api.github.com" {
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, metadataError(ctx, resource)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s not found (HTTP 404)", resource)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, fmt.Errorf("%s request denied or rate limited (HTTP %d); retry later or use an exact package version", resource, response.StatusCode)
	default:
		return nil, fmt.Errorf("%s returned HTTP %d", resource, response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", resource, limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, metadataError(ctx, resource)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", resource, limit)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", resource, err)
	}
	return data, nil
}

func metadataError(ctx context.Context, resource string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read %s: %w", resource, err)
	}
	// Transport errors can contain URLs and response data. Never return them.
	return fmt.Errorf("read %s failed", resource)
}
