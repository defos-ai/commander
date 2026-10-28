package tests

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/defos-ai/commander/apps/admin/ui/backend/connections"
	"github.com/defos-ai/commander/apps/admin/ui/backend/tests/fixtures"
)

func TestSecretAndCatalogMutationsUseAuthenticatedSameOriginProxy(t *testing.T) {
	type mutation struct{ method, path, body, contentType string }
	observed := make(chan mutation, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "operator" || password != "private-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/v1/missions" {
			io.WriteString(w, `{"items":[]}`)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("could not read proxied API mutation")
		}
		observed <- mutation{r.Method, r.RequestURI, string(body), r.Header.Get("Content-Type")}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"metadata-only"}`)
	}))
	defer upstream.Close()
	handler, err := connections.NewHandler(connections.Config{APIUpstreams: fixtures.LocalMappings(upstream.URL)})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	cookie := fixtures.Login(t, handler, upstream.URL, nil)

	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPut, "/api/v1/secrets/GITHUB_TOKEN", `{"id":"00000000-0000-4000-8000-000000000001","version":1,"value":"private-test-value"}`, http.StatusOK},
		{http.MethodDelete, "/api/v1/secrets/GITHUB_TOKEN?id=00000000-0000-4000-8000-000000000001&version=2", "", http.StatusNoContent},
		{http.MethodPatch, "/api/v1/agent-sources/github.com%2facme%2fworker-agent@v1.2.3", `{"trusted":true,"version":1}`, http.StatusOK},
	} {
		t.Run(tc.method, func(t *testing.T) {
			path := fixtures.ProxyPath(upstream.URL, tc.path)
			for _, boundary := range []string{"no origin", "cross origin", "no session"} {
				request := fixtures.BrowserRequest(tc.method, path, tc.body, cookie)
				status := http.StatusForbidden
				switch boundary {
				case "no origin":
					request.Header.Del("Origin")
				case "cross origin":
					request.Header.Set("Origin", "https://attacker.example")
				case "no session":
					request.Header.Del("Cookie")
					status = http.StatusUnauthorized
				}
				result := fixtures.ServeUI(handler, request)
				if result.Code != status || len(observed) != 0 {
					t.Fatalf("%s did not reject the API mutation before forwarding", boundary)
				}
			}
			result := fixtures.ServeUI(handler, fixtures.BrowserRequest(tc.method, path, tc.body, cookie))
			if result.Code != tc.status || len(observed) != 1 || result.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("API mutation was not forwarded exactly once with uncacheable response")
			}
			request := <-observed
			if request.method != tc.method || request.path != tc.path || request.body != tc.body || request.contentType != "application/json" {
				t.Fatal("proxy changed API mutation method, escaped path, identity, version or body")
			}
			if tc.method == http.MethodDelete && result.Body.Len() != 0 {
				t.Fatal("delete response must have no body")
			}
		})
	}
}
