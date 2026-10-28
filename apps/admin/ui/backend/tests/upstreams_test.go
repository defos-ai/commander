package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/apps/admin/ui/backend/connections"
	"github.com/defos-ai/commander/apps/admin/ui/backend/tests/fixtures"
)

func TestAPIUpstreamRouting(t *testing.T) {
	var directCalls atomic.Int64
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directCalls.Add(1)
		io.WriteString(w, "direct")
	}))
	defer direct.Close()
	other := httptest.NewServer(direct.Config.Handler)
	defer other.Close()
	observed := make(chan *http.Request, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Clone(r.Context())
		if strings.HasSuffix(r.URL.Path, "/aboutz") {
			io.WriteString(w, `{"runtime":"docker-compose","application":"commander-api"}`)
			return
		}
		io.WriteString(w, `{"items":[]}`)
	}))
	defer upstream.Close()
	publicURL := direct.URL + "/public%2Fbase"
	upstreamURL := upstream.URL + "/public%2Fbase"
	upstreamHost := strings.TrimPrefix(upstream.URL, "http://")
	handler, err := connections.NewHandler(connections.Config{
		APIUpstreams: strings.TrimPrefix(strings.Replace(direct.URL, "127.0.0.1:", "127.0.0.1:0", 1), "http://") + "=" + upstreamHost +
			",LOCALHOST:00080=" + upstreamHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	response := fixtures.ServeUI(handler, fixtures.BrowserRequest(http.MethodGet, fixtures.ProxyPath(publicURL+"/", "/healthz"), "", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("mapped health failed: %d %s", response.Code, response.Body.String())
	}
	forwarded := <-observed
	if forwarded.RequestURI != "/public%2Fbase/healthz" || forwarded.Header.Get("Authorization") != "" {
		t.Fatalf("mapped health request changed: %s %v", forwarded.RequestURI, forwarded.Header)
	}
	checkAbout := func(cookie *http.Cookie) {
		t.Helper()
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			request := fixtures.BrowserRequest(method, fixtures.ProxyPath(publicURL, "/aboutz"), "", cookie)
			request.SetBasicAuth("browser-user", "must-not-be-forwarded")
			request.AddCookie(&http.Cookie{Name: "browser-cookie", Value: "must-not-be-forwarded"})
			response := fixtures.ServeUI(handler, request)
			if response.Code != http.StatusOK {
				t.Fatalf("mapped about discovery failed: %d", response.Code)
			}
			if method == http.MethodGet && response.Body.String() != `{"runtime":"docker-compose","application":"commander-api"}` {
				t.Fatalf("about metadata changed: %s", response.Body.String())
			}
			if method == http.MethodHead && response.Body.Len() != 0 {
				t.Fatal("HEAD about response must be empty")
			}
			forwarded := <-observed
			if forwarded.RequestURI != "/public%2Fbase/aboutz" || forwarded.Method != method {
				t.Fatalf("mapped about request changed: %s %s", forwarded.Method, forwarded.RequestURI)
			}
			if forwarded.Header.Get("Authorization") != "" || forwarded.Header.Get("Cookie") != "" {
				t.Fatal("public about request leaked browser or saved API credentials")
			}
		}
	}
	checkAbout(nil)
	response = fixtures.ServeUI(handler, fixtures.BrowserRequest(http.MethodPost, "/ui/connections", fixtures.LoginBody(publicURL), nil))
	var connection connections.Connection
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &connection) != nil || connection.URL != publicURL || len(response.Result().Cookies()) != 1 {
		t.Fatalf("mapped login must return the public URL: %d %s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	forwarded = <-observed
	user, password, ok := forwarded.BasicAuth()
	if forwarded.RequestURI != "/public%2Fbase/api/v1/missions" || !ok || user != "operator" || password != "private-password" {
		t.Fatalf("mapped authentication changed: %s", forwarded.RequestURI)
	}
	checkAbout(cookie)
	response = fixtures.ServeUI(handler, fixtures.BrowserRequest(http.MethodGet, fixtures.ProxyPath(publicURL, "/api/v1/agents/team%2Fagent?limit=5&label=a%2Fb"), "", cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("mapped proxy failed: %d %s", response.Code, response.Body.String())
	}
	forwarded = <-observed
	user, password, ok = forwarded.BasicAuth()
	if forwarded.RequestURI != "/public%2Fbase/api/v1/agents/team%2Fagent?limit=5&label=a%2Fb" || !ok || user != "operator" || password != "private-password" {
		t.Fatalf("mapped proxy changed path, query, or credentials: %s", forwarded.RequestURI)
	}
	response = fixtures.ServeUI(handler, fixtures.BrowserRequest(http.MethodGet, fixtures.ProxyPath(upstreamURL, "/api/v1/missions"), "", cookie))
	if response.Code != http.StatusUnauthorized {
		t.Fatal("public session credentials must not be keyed by the private upstream")
	}
	if directCalls.Load() != 0 {
		t.Fatal("mapped requests reached the public address")
	}
	aliasURL := "http://localhost/public%2Fbase"
	response = fixtures.ServeUI(handler, fixtures.BrowserRequest(http.MethodGet, fixtures.ProxyPath(aliasURL, "/api/v1/missions"), "", cookie))
	if response.Code != http.StatusUnauthorized {
		t.Fatal("mapped aliases must not share public session credentials")
	}
	cookie = fixtures.LoginAs(t, handler, aliasURL, cookie, "alias-operator", "alias-password")
	<-observed
	assertConnections(t, handler, cookie, 2)
	for target, wantUser := range map[string]string{publicURL: "operator", aliasURL: "alias-operator"} {
		response = fixtures.ServeUI(handler, fixtures.BrowserRequest(http.MethodGet, fixtures.ProxyPath(target, "/api/v1/missions"), "", cookie))
		if response.Code != http.StatusOK {
			t.Fatalf("mapped alias failed: %s %d", target, response.Code)
		}
		forwarded = <-observed
		user, _, _ = forwarded.BasicAuth()
		if user != wantUser {
			t.Fatalf("mapped alias used another API's credentials: %s got %s", target, user)
		}
	}
	for _, target := range []string{direct.URL + "/public/base", publicURL + "/other"} {
		response = fixtures.ServeUI(handler, fixtures.BrowserRequest(http.MethodGet, fixtures.ProxyPath(target, "/healthz"), "", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("mapped hostname did not retain another base path: %s %d", target, response.Code)
		}
		forwarded = <-observed
		if forwarded.RequestURI != strings.TrimPrefix(target, direct.URL)+"/healthz" {
			t.Fatalf("mapping changed the public base path: %s", forwarded.RequestURI)
		}
	}
	for _, target := range []string{
		other.URL + "/public%2Fbase",
		strings.Replace(publicURL, "127.0.0.1", "localhost", 1),
	} {
		response = fixtures.ServeUI(handler, fixtures.BrowserRequest(http.MethodGet, fixtures.ProxyPath(target, "/healthz"), "", nil))
		if response.Code != http.StatusForbidden {
			t.Fatalf("unmapped internal API escaped the network policy: %s: %d %s", target, response.Code, response.Body.String())
		}
	}
	upstream.Close()
	for _, request := range []*http.Request{
		fixtures.BrowserRequest(http.MethodGet, fixtures.ProxyPath(publicURL, "/healthz"), "", nil),
		fixtures.BrowserRequest(http.MethodPost, "/ui/connections", fixtures.LoginBody(publicURL), nil),
	} {
		response = fixtures.ServeUI(handler, request)
		if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), upstreamURL) {
			t.Fatalf("unavailable upstream response: %d %s", response.Code, response.Body.String())
		}
	}
	if directCalls.Load() != 0 {
		t.Fatal("upstream failure must not fall back to the public address")
	}
}

func TestAPIUpstreamConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, upstream string
		invalid        bool
	}{
		{name: "native defaults"},
		{name: "IPv6 map", upstream: "[::1]:4274=[::2]:4274"},
		{name: "upstream scheme", upstream: "localhost:4274=http://api:4274", invalid: true},
		{name: "upstream query", upstream: "localhost:4274=api:4274?token=secret", invalid: true},
		{name: "upstream path", upstream: "localhost:4274=api:4274/a", invalid: true},
		{name: "upstream credentials", upstream: "localhost:4274=user:password@api:4274", invalid: true},
		{name: "upstream port", upstream: "localhost:4274=api:65536", invalid: true},
		{name: "missing public port", upstream: "localhost=api:4274", invalid: true},
		{name: "duplicate key", upstream: "LOCALHOST:04274=api:4274,localhost:4274=other:4274", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, err := connections.NewHandler(connections.Config{APIUpstreams: test.upstream})
			if test.invalid {
				if err == nil {
					handler.Close()
					t.Fatal("invalid configuration accepted")
				}
				if !strings.Contains(err.Error(), "api-upstreams") {
					t.Fatalf("error must identify the invalid field: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			handler.Close()
		})
	}
}
