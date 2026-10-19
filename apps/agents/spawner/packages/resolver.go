package packages

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/defos-ai/commander/framework/agent/manifest"
	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
)

type Package struct {
	Reference agentpackages.Reference
	Manifest  manifest.Manifest
}

type Resolver struct {
	client *http.Client
}

// NewResolver uses a dedicated transport without environment proxies or cookies.
// A supplied transport is trusted deployment configuration (for example, custom
// TLS roots); it must not add credentials to these public download requests.
func NewResolver(transport http.RoundTripper) *Resolver {
	if transport == nil {
		transport = &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		}
	}
	return &Resolver{client: &http.Client{Transport: transport, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many package download redirects")
		}
		// Redirect URLs can contain GitHub's signed query parameters. Never add
		// cookies or deployment credentials, even when the host remains GitHub.
		request.Header.Del("Authorization")
		request.Header.Del("Proxy-Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Referer")
		return validateDownloadURL(request.URL)
	}}}
}

func (r *Resolver) CloseIdleConnections() { r.client.CloseIdleConnections() }

func (r *Resolver) Resolve(ctx context.Context, reference string) (Package, error) {
	ref, err := agentpackages.ParseReference(reference)
	if err != nil {
		return Package{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := r.get(ctx, assetURL(ref, "agent.yaml"))
	if err != nil {
		return Package{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, manifest.MaxBytes+1))
	if err != nil {
		return Package{}, downloadError(ctx, "read package manifest")
	}
	parsed, err := manifest.Parse(data, ref.Version)
	if err != nil {
		return Package{}, err
	}
	return Package{Reference: ref, Manifest: parsed}, nil
}

func (r *Resolver) get(ctx context.Context, target string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, errors.New("invalid package download URL")
	}
	if err := validateDownloadURL(request.URL); err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Commander-Spawner")
	response, err := r.client.Do(request)
	if err != nil {
		// http.Client errors include redirect URLs and possibly signed queries.
		return nil, downloadError(ctx, "download package asset")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("package download returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func validateDownloadURL(target *url.URL) error {
	if target.Scheme != "https" || target.User != nil || target.Opaque != "" || target.Fragment != "" || (target.Port() != "" && target.Port() != "443") {
		return errors.New("package downloads require HTTPS without URL credentials")
	}
	switch target.Hostname() {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return nil
	default:
		return errors.New("package download redirect must stay on GitHub release hosts")
	}
}

func downloadError(ctx context.Context, action string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s failed", action)
}

func assetURL(ref agentpackages.Reference, asset string) string {
	return "https://github.com/" + ref.Owner + "/" + ref.Repository + "/releases/download/" + url.PathEscape(ref.ReleaseTag()) + "/" + url.PathEscape(asset)
}
