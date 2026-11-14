package fakes

import (
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
)

type InspectionRequest struct {
	URL, Accept                     string
	HasAuthorization, Authenticated bool
}

type InspectionSource struct {
	manifest string
	token    string
	requests []InspectionRequest
	tokens   [][]byte
}

func NewInspectionSource(manifest, token string) *InspectionSource {
	return &InspectionSource{manifest: manifest, token: token}
}

func (s *InspectionSource) RoundTrip(request *http.Request) (*http.Response, error) {
	authorization := request.Header.Get("Authorization")
	s.requests = append(s.requests, InspectionRequest{
		URL: request.URL.String(), Accept: request.Header.Get("Accept"),
		HasAuthorization: authorization != "", Authenticated: s.token != "" && authorization == "Bearer "+s.token,
	})
	s.tokens = append(s.tokens, packages.GitHubToken(request.Context()))
	status, body := http.StatusOK, ""
	switch request.URL.String() {
	case "https://api.github.com/repos/other/worker-agent/releases/latest", "https://api.github.com/repos/other/worker-agent/releases/tags/v1.2.3":
		body = `{"tag_name":"v1.2.3","draft":false,"prerelease":false,"published_at":"2026-01-01T00:00:00Z","assets":[{"id":77,"name":"agent.yaml","url":"https://example.invalid/credential-leak"}]}`
	case "https://github.com/other/worker-agent/releases/download/v1.2.3/agent.yaml", "https://api.github.com/repos/other/worker-agent/releases/assets/77":
		body = s.manifest
	default:
		status = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)), Request: request,
	}, nil
}

func (s *InspectionSource) Requests() []InspectionRequest { return slices.Clone(s.requests) }

func (s *InspectionSource) CredentialsCleared() bool {
	for _, token := range s.tokens {
		for _, value := range token {
			if value != 0 {
				return false
			}
		}
	}
	return true
}
