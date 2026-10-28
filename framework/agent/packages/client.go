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
)

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
		// A redirect must not silently change the configured sources URL.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *Client) CloseIdleConnections() { c.client.CloseIdleConnections() }

// LoadSources reads a host-configured HTTPS source. Do not pass model/user URLs
// here: selecting the sources is deployment policy, not installation input.
func (c *Client) LoadSources(ctx context.Context, source string) (Sources, error) {
	target, err := url.Parse(source)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Opaque != "" || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" {
		return Sources{}, errors.New("sources URL must be an HTTPS URL without credentials, query or fragment")
	}
	data, err := c.read(ctx, source, "agent sources", MaxSourcesBytes)
	if err != nil {
		return Sources{}, err
	}
	sources, err := ParseSources(data)
	if err != nil {
		return Sources{}, errors.New("invalid agent sources")
	}
	return sources, nil
}

func (c *Client) read(ctx context.Context, target, resource string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid %s URL", resource)
	}
	request.Header.Set("User-Agent", "Commander-Agent-Sources")
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
		return nil, fmt.Errorf("%s request denied or rate limited (HTTP %d); retry later", resource, response.StatusCode)
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
