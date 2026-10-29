package fakes

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ApprovalSource struct {
	requests int
	latest   int
}

func (s *ApprovalSource) RoundTrip(request *http.Request) (*http.Response, error) {
	s.requests++
	if request.URL.String() != "https://api.github.com/repos/other/worker-agent/releases/latest" {
		return nil, errors.New("unexpected source metadata request")
	}
	minor := max(1, s.latest)
	body := fmt.Sprintf(`{"tag_name":"v0.%d.0","draft":false,"prerelease":false,"published_at":"2026-10-01T12:00:00Z"}`, minor)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func (s *ApprovalSource) RequestCount() int   { return s.requests }
func (s *ApprovalSource) SetLatest(minor int) { s.latest = minor }
