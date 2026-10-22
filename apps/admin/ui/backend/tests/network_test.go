package tests

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/apps/admin/ui/backend/connections"
)

func TestUIRejectsUnmappedInternalAPIsBeforeSendingCredentials(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"items":[]}`)
	}))
	defer upstream.Close()
	handler, err := connections.NewHandler(connections.Config{APIUpstreams: "api.example:80=" + strings.TrimPrefix(upstream.URL, "http://")})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	// Populate the mapped connection pool before trying its private address directly.
	cookie := login(t, handler, "http://api.example", nil)
	for _, target := range []string{upstream.URL, "http://10.0.0.1", "http://169.254.169.254", "http://[fd00:ec2::254]"} {
		for _, request := range []*http.Request{
			browserRequest(http.MethodGet, proxyPath(target, "/healthz"), "", cookie),
			browserRequest(http.MethodGet, proxyPath(target, "/aboutz"), "", cookie),
			browserRequest(http.MethodPost, "/ui/connections", loginBody(target), cookie),
		} {
			response := serveUI(handler, request)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "UI_API_UPSTREAMS") {
				t.Fatalf("unsafe API target was not rejected: %d %s", response.Code, response.Body.String())
			}
		}
	}
	if calls.Load() != 1 {
		t.Fatal("an unmapped internal API received a probe or credentials")
	}
	response := serveUI(handler, browserRequest(http.MethodGet, proxyPath("http://api.example", "/api/v1/missions"), "", cookie))
	if response.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatal("blocking unsafe targets must retain the mapped API session")
	}
}
