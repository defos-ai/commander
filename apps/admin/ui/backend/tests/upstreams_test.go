package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"commander/admin/ui/connections"
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
	response := serveUI(handler, browserRequest(http.MethodGet, proxyPath(publicURL+"/", "/healthz"), "", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("mapped health failed: %d %s", response.Code, response.Body.String())
	}
	forwarded := <-observed
	if forwarded.RequestURI != "/public%2Fbase/healthz" || forwarded.Header.Get("Authorization") != "" {
		t.Fatalf("mapped health request changed: %s %v", forwarded.RequestURI, forwarded.Header)
	}
	response = serveUI(handler, browserRequest(http.MethodPost, "/ui/connections", loginBody(publicURL), nil))
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
	response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(publicURL, "/api/v1/agents/team%2Fagent?limit=5&label=a%2Fb"), "", cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("mapped proxy failed: %d %s", response.Code, response.Body.String())
	}
	forwarded = <-observed
	user, password, ok = forwarded.BasicAuth()
	if forwarded.RequestURI != "/public%2Fbase/api/v1/agents/team%2Fagent?limit=5&label=a%2Fb" || !ok || user != "operator" || password != "private-password" {
		t.Fatalf("mapped proxy changed path, query, or credentials: %s", forwarded.RequestURI)
	}
	response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(upstreamURL, "/api/v1/missions"), "", cookie))
	if response.Code != http.StatusUnauthorized {
		t.Fatal("public session credentials must not be keyed by the private upstream")
	}
	if directCalls.Load() != 0 {
		t.Fatal("mapped requests reached the public address")
	}
	aliasURL := "http://localhost/public%2Fbase"
	response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(aliasURL, "/api/v1/missions"), "", cookie))
	if response.Code != http.StatusUnauthorized {
		t.Fatal("mapped aliases must not share public session credentials")
	}
	cookie = loginAs(t, handler, aliasURL, cookie, "alias-operator", "alias-password")
	<-observed
	assertConnections(t, handler, cookie, 2)
	for target, wantUser := range map[string]string{publicURL: "operator", aliasURL: "alias-operator"} {
		response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(target, "/api/v1/missions"), "", cookie))
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
		response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(target, "/healthz"), "", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("mapped hostname did not retain another base path: %s %d", target, response.Code)
		}
		forwarded = <-observed
		if forwarded.RequestURI != strings.TrimPrefix(target, direct.URL)+"/healthz" {
			t.Fatalf("mapping changed the public base path: %s", forwarded.RequestURI)
		}
	}
	for _, target := range []string{
		other.URL + "/public%2Fbase", // The same hostname on another port stays direct.
		strings.Replace(publicURL, "127.0.0.1", "localhost", 1),
	} {
		response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(target, "/healthz"), "", nil))
		if response.Code != http.StatusOK || response.Body.String() != "direct" {
			t.Fatalf("different API was rewritten: %s: %d %s", target, response.Code, response.Body.String())
		}
	}
	upstream.Close()
	for _, request := range []*http.Request{
		browserRequest(http.MethodGet, proxyPath(publicURL, "/healthz"), "", nil),
		browserRequest(http.MethodPost, "/ui/connections", loginBody(publicURL), nil),
	} {
		response = serveUI(handler, request)
		if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), upstreamURL) {
			t.Fatalf("unavailable upstream response: %d %s", response.Code, response.Body.String())
		}
	}
	if directCalls.Load() != 2 {
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
