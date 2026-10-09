package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type AuthenticationError struct {
	Command string
}

func (*AuthenticationError) Error() string { return errAuthentication.Error() }
func (*AuthenticationError) Unwrap() error { return errAuthentication }

func authenticationError(ctx context.Context, endpoint string) error {
	return &AuthenticationError{Command: credentialCommand(ctx, endpoint)}
}

func credentialCommand(ctx context.Context, endpoint string) string {
	base, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	base.RawPath = ""
	about := base.ResolveReference(&url.URL{Path: "aboutz"})
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, about.String(), nil)
	if err != nil {
		return ""
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	const limit = 4096
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(body) > limit {
		return ""
	}
	var deployment struct {
		Runtime     string `json:"runtime"`
		Application string `json:"application"`
	}
	if json.Unmarshal(body, &deployment) != nil {
		return ""
	}
	if deployment.Application == "commander" {
		switch deployment.Runtime {
		case "native":
			return "/credentials"
		case "docker":
			return "docker exec commander commander credentials mcp"
		}
	}
	if deployment.Application == "commander-mcp" {
		switch deployment.Runtime {
		case "native":
			return "./commander-mcp credentials"
		case "docker":
			return "docker exec commander-mcp commander-mcp credentials"
		case "docker-compose":
			return "docker compose exec mcp commander-mcp credentials"
		}
	}
	return ""
}
