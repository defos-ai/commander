// Package api supplies the local secret form's authenticated API connection.
// Secret values never pass through the chat engine or MCP.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
)

var (
	ErrAuthentication = errors.New("API login rejected; enter the API username and password")
	ErrConflict       = errors.New("secret changed; reopen the form to load its current version")
	ErrWriteUncertain = errors.New("secret save could not be confirmed; check Admin before submitting again")
)

const maxMetadataBytes = 64 << 10

// Metadata contains only the information needed to replace the exact saved secret.
type Metadata struct {
	ID        string     `json:"id"`
	AgentType string     `json:"agent_type"`
	Name      string     `json:"name"`
	Version   uint64     `json:"version"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type Client struct {
	endpoint    string
	credentials host.BasicCredentials
	http        *http.Client
}

// New accepts an operator-configured API base URL, never one supplied by a model
// or inferred from a remote MCP address. An optional reverse-proxy prefix is kept.
func New(endpoint string, credentials host.BasicCredentials) (*Client, error) {
	if err := ValidateURL(endpoint); err != nil {
		return nil, err
	}
	if credentials.Validate() != nil {
		return nil, errors.New("API username and password must be set and valid")
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          2,
		ForceAttemptHTTP2:     true,
	}
	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"), credentials: credentials,
		http: &http.Client{
			Transport: transport, Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func ValidateURL(endpoint string) error {
	base, err := url.Parse(endpoint)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") ||
		base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || strings.Contains(endpoint, "#") || base.Opaque != "" {
		return errors.New("API URL must use HTTP or HTTPS without credentials, a query or a fragment")
	}
	return nil
}

func (*Client) String() string   { return "Commander API client" }
func (*Client) GoString() string { return "Commander API client" }
func (c *Client) Close()         { c.http.CloseIdleConnections() }

// Metadata returns nil when the scoped secret has not been saved.
func (c *Client) Metadata(ctx context.Context, key secrets.Key) (*Metadata, error) {
	if key.Validate() != nil {
		return nil, errors.New("invalid secret scope or name")
	}
	return c.request(ctx, http.MethodGet, scopedPath(key), key, nil)
}

// Save creates a missing secret or replaces only the version shown in the form.
// An ambiguous response is never retried: the API may already have saved it.
func (c *Client) Save(ctx context.Context, key secrets.Key, value string, expected *Metadata) error {
	if key.Validate() != nil {
		return errors.New("invalid secret scope or name")
	}
	method, path := http.MethodPost, "/api/v1/secrets"
	var body []byte
	var err error
	if expected == nil {
		body, err = json.Marshal(struct {
			AgentType string `json:"agent_type"`
			Name      string `json:"name"`
			Value     string `json:"value"`
		}{key.Scope.AgentType, key.Name, value})
	} else {
		if !validMetadata(expected, key) || expected.Version == ^uint64(0) {
			return errors.New("invalid saved secret metadata; reopen the form")
		}
		method, path = http.MethodPut, scopedPath(key)
		// Keep a future expiry when rotating a value. An expired secret is renewed
		// without an expiry, as when a new value is supplied through Admin.
		var expiresAt *time.Time
		if expected.ExpiresAt != nil && expected.ExpiresAt.After(time.Now()) {
			expiresAt = expected.ExpiresAt
		}
		body, err = json.Marshal(struct {
			ID        string     `json:"id"`
			Version   uint64     `json:"version"`
			Value     string     `json:"value"`
			ExpiresAt *time.Time `json:"expires_at,omitempty"`
		}{expected.ID, expected.Version, value, expiresAt})
	}
	if err != nil || len(body) > 1<<20 {
		clear(body)
		return errors.New("secret value is too large")
	}
	metadata, err := c.request(ctx, method, path, key, body)
	if err != nil {
		return err
	}
	if (expected == nil && metadata.Version != 1) ||
		(expected != nil && (metadata.ID != expected.ID || metadata.Version != expected.Version+1)) {
		return ErrWriteUncertain
	}
	return nil
}

func scopedPath(key secrets.Key) string {
	return "/api/v1/secrets/" + url.PathEscape(key.Scope.AgentType) + "/" + url.PathEscape(key.Name)
}

func (c *Client) request(ctx context.Context, method, path string, key secrets.Key, body []byte) (*Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("could not create API request")
	}
	request.SetBasicAuth(c.credentials.Username, c.credentials.Password)
	request.Header.Set("Accept", "application/json")
	if method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if method != http.MethodGet {
			return nil, errors.Join(ErrWriteUncertain, ctx.Err())
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not connect to API; check the API URL")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound && method == http.MethodGet {
		return nil, nil
	}
	expectedStatus := http.StatusOK
	if method == http.MethodPost {
		expectedStatus = http.StatusCreated
	}
	if response.StatusCode != expectedStatus {
		return nil, statusError(response.StatusCode, method != http.MethodGet)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxMetadataBytes+1))
	defer clear(data)
	var metadata Metadata
	if err != nil || len(data) > maxMetadataBytes || json.Unmarshal(data, &metadata) != nil || !validMetadata(&metadata, key) {
		if method != http.MethodGet {
			return nil, errors.Join(ErrWriteUncertain, ctx.Err())
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("API returned invalid secret metadata")
	}
	return &metadata, nil
}

func validMetadata(metadata *Metadata, key secrets.Key) bool {
	return id.Valid(metadata.ID) && metadata.AgentType == key.Scope.AgentType && metadata.Name == key.Name &&
		metadata.Version > 0 && (metadata.Status == "available" || metadata.Status == "expired") &&
		(metadata.ExpiresAt == nil || !metadata.ExpiresAt.IsZero())
}

func statusError(status int, mutation bool) error {
	switch {
	case status == http.StatusUnauthorized:
		return ErrAuthentication
	case status == http.StatusForbidden:
		return errors.New("API access denied")
	case status == http.StatusConflict || (mutation && status == http.StatusNotFound):
		return ErrConflict
	case status >= 300 && status < 400:
		return errors.New("API redirects are disabled; use the direct API URL")
	case status == http.StatusBadRequest || status == http.StatusRequestEntityTooLarge:
		return errors.New("API rejected the secret; check its value and configuration")
	case mutation:
		return ErrWriteUncertain
	default:
		return fmt.Errorf("API secret lookup failed (HTTP %d)", status)
	}
}
