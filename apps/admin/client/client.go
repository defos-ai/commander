// Package client supplies authenticated API transport for administrative commands.
package client

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
	"unicode"

	"github.com/defos-ai/commander/framework/host"
)

const (
	maxResponseBytes = 4 << 20
	maxErrorBytes    = 64 << 10
)

var (
	ErrAuthentication   = errors.New("API login rejected; check the API username and password")
	ErrWriteUncertain   = errors.New("API write could not be confirmed; check saved state before submitting again")
	ErrInvalidResponse  = errors.New("API returned an invalid JSON response")
	ErrResponseTooLarge = errors.New("API response exceeds the configured size limit")
)

type Connection struct {
	URL         string
	Credentials host.BasicCredentials
}

// ConnectionError lets an embedded client request local connection settings.
type ConnectionError struct{ cause error }

func (e *ConnectionError) Error() string { return e.cause.Error() }
func (e *ConnectionError) Unwrap() error { return e.cause }

func (Connection) String() string   { return "Commander API connection" }
func (Connection) GoString() string { return "Commander API connection" }

// StatusError exposes a bounded API error identifier for programmatic handling.
// Diagnostics deliberately omit remote messages, identifiers and redirect URLs.
type StatusError struct {
	StatusCode int
	Code       string
	mutation   bool
}

func (e *StatusError) Error() string {
	switch {
	case e.StatusCode == http.StatusUnauthorized:
		return ErrAuthentication.Error()
	case e.StatusCode >= 300 && e.StatusCode < 400:
		return "API redirects are disabled; use the direct API URL"
	case e.mutation && e.StatusCode >= 500:
		return ErrWriteUncertain.Error()
	default:
		return fmt.Sprintf("API request failed (HTTP %d)", e.StatusCode)
	}
}

func (e *StatusError) Is(target error) bool {
	return target == ErrAuthentication && e.StatusCode == http.StatusUnauthorized ||
		target == ErrWriteUncertain && e.mutation && e.StatusCode >= 500
}

type Client struct {
	endpoint    string
	credentials host.BasicCredentials
	http        *http.Client
}

// Response contains a successful response. The caller owns and may clear Data.
type Response struct {
	StatusCode int
	Data       []byte
}

// New accepts an operator-configured API URL, including a reverse-proxy prefix.
// It never derives the destination from model output or a remote MCP address.
func New(connection Connection) (*Client, error) {
	if err := ValidateURL(connection.URL); err != nil {
		return nil, &ConnectionError{cause: err}
	}
	if connection.Credentials.Validate() != nil {
		return nil, &ConnectionError{cause: errors.New("API username and password must be set and valid")}
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
		endpoint: strings.TrimRight(connection.URL, "/"), credentials: connection.Credentials,
		http: &http.Client{
			Transport: transport, Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func ValidateURL(endpoint string) error {
	base, err := url.Parse(endpoint)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") ||
		base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery ||
		base.Fragment != "" || strings.Contains(endpoint, "#") || base.Opaque != "" {
		return errors.New("API URL must use HTTP or HTTPS without credentials, a query or a fragment")
	}
	return nil
}

func (*Client) String() string   { return "Commander API client" }
func (*Client) GoString() string { return "Commander API client" }
func (c *Client) Close()         { c.http.CloseIdleConnections() }

// Request sends one JSON request. The caller owns body and any returned bytes.
// Mutation failures may represent a completed write; this client never retries them.
func (c *Client) Request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	response, err := c.Do(ctx, method, path, body, maxResponseBytes)
	return response.Data, err
}

// Do sends one JSON request with a response limit between 1 byte and 4 MiB.
// Mutation failures may represent a completed write; they are never retried.
func (c *Client) Do(ctx context.Context, method, path string, body []byte, responseLimit int) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if !validMethod(method) || !validPath(path) {
		return Response{}, errors.New("invalid API request method or path")
	}
	if responseLimit < 1 || responseLimit > maxResponseBytes {
		return Response{}, errors.New("API response limit must be between 1 byte and 4 MiB")
	}
	if len(body) > maxResponseBytes || len(body) > 0 && !json.Valid(body) {
		return Response{}, errors.New("API request body must be JSON within the 4 MiB limit")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return Response{}, errors.New("could not create API request")
	}
	request.SetBasicAuth(c.credentials.Username, c.credentials.Password)
	request.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	mutation := method != http.MethodGet && method != http.MethodHead
	response, err := c.http.Do(request)
	if err != nil {
		return Response{}, requestFailure(ctx, mutation, errors.New("could not connect to API; check the API URL"))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Response{}, responseError(response, mutation)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(responseLimit)+1))
	if err != nil {
		clear(data)
		return Response{}, requestFailure(ctx, mutation, errors.New("could not read the API response"))
	}
	if len(data) > responseLimit {
		clear(data)
		return Response{}, requestFailure(ctx, mutation, ErrResponseTooLarge)
	}
	if method != http.MethodHead && response.StatusCode != http.StatusNoContent && !json.Valid(data) {
		clear(data)
		return Response{}, requestFailure(ctx, mutation, ErrInvalidResponse)
	}
	return Response{StatusCode: response.StatusCode, Data: data}, nil
}

func requestFailure(ctx context.Context, mutation bool, failure error) error {
	if mutation {
		return errors.Join(ErrWriteUncertain, ctx.Err(), failure)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return failure
}

func responseError(response *http.Response, mutation bool) error {
	result := &StatusError{StatusCode: response.StatusCode, mutation: mutation}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBytes+1))
	defer clear(data)
	if err != nil || len(data) > maxErrorBytes {
		return result
	}
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && validCode(body.Error) {
		result.Code = body.Error
	}
	return result
}

func validCode(code string) bool {
	if len(code) == 0 || len(code) > 64 || code[0] < 'a' || code[0] > 'z' {
		return false
	}
	for _, r := range code {
		if r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func validPath(path string) bool {
	relative, err := url.Parse(path)
	if err != nil || relative.Scheme != "" || relative.Host != "" || relative.User != nil ||
		relative.Opaque != "" || relative.Fragment != "" || strings.Contains(path, "#") ||
		strings.Contains(path, "\\") || strings.ContainsFunc(path, unicode.IsControl) {
		return false
	}
	escaped := relative.EscapedPath()
	if escaped != "/api/v1" && !strings.HasPrefix(escaped, "/api/v1/") {
		return false
	}
	// Escaped path segments are parameter data, even when their decoded names
	// contain slashes or percent signs. Only literal segments change structure.
	for _, segment := range strings.Split(escaped, "/")[1:] {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return !strings.Contains(escaped, "//")
}
