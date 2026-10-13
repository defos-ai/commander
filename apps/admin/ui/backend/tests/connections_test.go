package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/admin/ui/backend/connections"
)

func TestBrowserSessionConnectionsAndProxy(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	observed := make(chan *http.Request, 32)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Clone(r.Context())
		username, password, ok := r.BasicAuth()
		expectedUsername, expectedPassword := "operator", "private-password"
		if strings.HasPrefix(r.URL.Path, "/second/") {
			expectedUsername, expectedPassword = "second-operator", "second-private-password"
		}
		if !ok || username != expectedUsername || password != expectedPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/missions"):
			io.WriteString(w, "{\"items\":[]}")
		case strings.HasSuffix(r.URL.Path, "/files/image"):
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Disposition", "attachment; filename=image.png")
			w.Header().Set("Set-Cookie", "upstream-secret=forbidden")
			w.Header().Set("Location", "https://must-not-escape.example")
			w.Write([]byte{0, 1, 2, 255})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations"):
			w.WriteHeader(http.StatusAccepted)
			io.Copy(w, r.Body)
		default:
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, "{\"error\":{\"message\":\"operation already finished\"}}")
		}
	}))
	defer upstream.Close()
	other := httptest.NewServer(upstream.Config.Handler)
	defer other.Close()
	firstURL, secondURL := "http://localhost:4274/first", other.URL+"/second"
	handler, err := connections.NewHandler(connections.Config{
		APIUpstreams: "localhost:4274=" + strings.TrimPrefix(upstream.URL, "http://") + "," + localMappings(upstream.URL, other.URL),
		Now:          func() time.Time { return time.Unix(0, now.Load()) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	first := login(t, handler, firstURL+"/", nil)
	if first.HttpOnly != true || first.SameSite != http.SameSiteStrictMode || first.Path != "/ui/" || first.Secure || len(first.Value) != 43 || first.MaxAge <= 0 || first.MaxAge > 8*60*60 {
		t.Fatalf("unexpected session cookie: %+v", first)
	}
	now.Add(int64(time.Hour))
	second := loginAs(t, handler, secondURL, first, "second-operator", "second-private-password")
	if second.Value == first.Value || !second.Expires.Equal(first.Expires) {
		t.Fatal("login must rotate the token without extending its absolute expiration")
	}
	assertConnections(t, handler, second, 2)
	assertConnections(t, handler, first, 0)
	listed := serveUI(handler, browserRequest(http.MethodGet, "/ui/connections", "", second)).Body.String()
	if !strings.Contains(listed, firstURL) || !strings.Contains(listed, secondURL) || strings.Contains(listed, upstream.URL) {
		t.Fatalf("connections must retain public URLs: %s", listed)
	}
	response := serveUI(handler, browserRequest(http.MethodGet, proxyPath(firstURL, "/api/v1/missions"), "", second))
	if response.Code != http.StatusOK {
		t.Fatal("adding another API replaced the first API's credentials")
	}

	request := browserRequest(http.MethodGet, proxyPath(secondURL, "/api/v1/artifacts/screenshot/files/image"), "", second)
	request.Header.Set("Authorization", "Basic browser-provided-value")
	request.AddCookie(&http.Cookie{Name: "unrelated", Value: "private-cookie"})
	response = serveUI(handler, request)
	if response.Code != http.StatusOK || response.Body.String() != string([]byte{0, 1, 2, 255}) || response.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("binary response changed: %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Disposition") != "attachment; filename=image.png" || response.Header().Get("Set-Cookie") != "" || response.Header().Get("Location") != "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unsafe or missing proxy headers: %v", response.Header())
	}
	var forwarded *http.Request
	for len(observed) > 0 {
		forwarded = <-observed
	}
	user, password, _ := forwarded.BasicAuth()
	if user != "second-operator" || password != "second-private-password" || forwarded.Header.Get("Cookie") != "" || forwarded.URL.Path != "/second/api/v1/artifacts/screenshot/files/image" {
		t.Fatal("proxy did not isolate browser credentials or preserve the API base path")
	}

	response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(secondURL, "/api/v1/agents/team%2Fagent%20%C3%A9?limit=5"), "", second))
	if response.Code != http.StatusConflict || response.Body.String() != "{\"error\":{\"message\":\"operation already finished\"}}" {
		t.Fatalf("upstream error changed: %d %s", response.Code, response.Body.String())
	}
	forwarded = <-observed
	if forwarded.RequestURI != "/second/api/v1/agents/team%2Fagent%20%C3%A9?limit=5" {
		t.Fatalf("escaped identifier changed: %s", forwarded.RequestURI)
	}
	command := "{\"mission_name\":\"review-website-with-browser\",\"input\":{\"url\":\"https://example.test\"}}"
	response = serveUI(handler, browserRequest(http.MethodPost, proxyPath(secondURL, "/api/v1/operations"), command, second))
	if response.Code != http.StatusAccepted || response.Body.String() != command || len(observed) != 1 {
		t.Fatal("proxy must forward a command once and preserve its response")
	}
	forwarded = <-observed
	if forwarded.Method != http.MethodPost || forwarded.Header.Get("Content-Type") != "application/json" {
		t.Fatal("proxy changed the command method or content type")
	}

	response = serveUI(handler, browserRequest(http.MethodDelete, "/ui/connections?url="+url.QueryEscape(firstURL), "", second))
	if response.Code != http.StatusNoContent || response.Header().Get("Set-Cookie") != "" {
		t.Fatal("forgetting one API must retain the other connection")
	}
	assertConnections(t, handler, second, 1)
	now.Add(int64(8 * time.Hour))
	assertConnections(t, handler, second, 0)
	response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(secondURL, "/api/v1/missions"), "", second))
	if response.Code != http.StatusUnauthorized || response.Header().Get("Set-Cookie") != "" {
		t.Fatal("expired session must reject access without clearing a newer browser cookie")
	}
	fresh := login(t, handler, firstURL, second)
	response = serveUI(handler, browserRequest(http.MethodDelete, "/ui/connections?url="+url.QueryEscape(firstURL), "", fresh))
	if response.Code != http.StatusNoContent || len(response.Result().Cookies()) != 1 || response.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("forgetting the last API must clear the session cookie")
	}
	assertConnections(t, handler, fresh, 0)

	encodedBase := upstream.URL + "/encoded%2Fapi"
	encodedCookie := login(t, handler, encodedBase, nil)
	response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(encodedBase, "/api/v1/missions"), "", encodedCookie))
	if response.Code != http.StatusOK {
		t.Fatal("escaped API base path did not retain its connection")
	}
	for len(observed) > 0 {
		forwarded = <-observed
	}
	if forwarded.RequestURI != "/encoded%2Fapi/api/v1/missions" {
		t.Fatalf("escaped API base changed: %s", forwarded.RequestURI)
	}
	response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(upstream.URL+"/encoded/api", "/api/v1/missions"), "", encodedCookie))
	if response.Code != http.StatusUnauthorized {
		t.Fatal("an escaped path must not share credentials with a different API URL")
	}
}

func TestBrowserSessionRequestBoundaries(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/healthz" {
			if r.Header.Get("Authorization") != "" {
				t.Error("public health request received credentials")
			}
			io.WriteString(w, "{\"status\":\"ok\"}")
			return
		}
		if _, password, _ := r.BasicAuth(); password != "private-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, "{\"items\":[]}")
	}))
	defer upstream.Close()
	handler, err := connections.NewHandler(connections.Config{APIUpstreams: localMappings(upstream.URL)})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	cookie := login(t, handler, upstream.URL, nil)
	initialCalls := calls.Load()
	for _, test := range []struct {
		name, method, path, body, header, value, host string
		status                                        int
	}{
		{name: "custom header required", path: "/ui/connections", header: "X-Commander-UI", status: 403},
		{name: "cross-origin rejected", path: "/ui/connections", header: "Origin", value: "http://attacker.example", status: 403},
		{name: "same-site is not same-origin", path: "/ui/connections", header: "Sec-Fetch-Site", value: "same-site", status: 403},
		{name: "null origin", path: "/ui/connections", header: "Origin", value: "null", status: 403},
		{name: "non-local HTTP", path: "/ui/connections", host: "admin.example", status: 403},
		{name: "mutation origin required", method: http.MethodDelete, path: "/ui/connections?url=" + url.QueryEscape(upstream.URL), header: "Origin", status: 403},
		{name: "invalid target scheme", path: proxyPath("ftp://invalid.example", "/healthz"), status: 400},
		{name: "credentials embedded in URL", path: proxyPath("http://user:password@invalid.example", "/healthz"), status: 400},
		{name: "credentials belong to exact URL", path: proxyPath(upstream.URL+"/other", "/api/v1/missions"), status: 401},
		{name: "removed target registry", path: "/ui/targets", status: 404},
		{name: "absolute path", path: proxyPath(upstream.URL, "https://evil.example/api/v1/missions"), status: 400},
		{name: "authority path", path: proxyPath(upstream.URL, "//evil.example/api/v1/missions"), status: 400},
		{name: "traversal", path: proxyPath(upstream.URL, "/api/v1/../../secret"), status: 400},
		{name: "encoded traversal", path: proxyPath(upstream.URL, "/api/v1/%2e%2e/secret"), status: 400},
		{name: "double encoded traversal", path: proxyPath(upstream.URL, "/api/v1/%252e%252e/secret"), status: 400},
		{name: "unknown endpoint", path: proxyPath(upstream.URL, "/admin/secret"), status: 400},
		{name: "about metadata is read-only", method: http.MethodPost, path: proxyPath(upstream.URL, "/aboutz"), status: 400},
		{name: "about subpath is not public", path: proxyPath(upstream.URL, "/aboutz/secret"), status: 400},
		{name: "about trailing slash is rejected", path: proxyPath(upstream.URL, "/aboutz/"), status: 400},
		{name: "about requires same origin", path: proxyPath(upstream.URL, "/aboutz"), header: "Origin", value: "http://attacker.example", status: 403},
		{name: "proxy delete unsupported", method: http.MethodDelete, path: proxyPath(upstream.URL, "/api/v1/missions"), status: 405},
		{name: "unknown UI route", path: "/ui/missing", status: 404},
		{name: "oversized login", method: http.MethodPost, path: "/ui/connections", body: "{\"password\":\"" + strings.Repeat("x", 65<<10) + "\"}", status: 413},
		{name: "oversized command", method: http.MethodPost, path: proxyPath(upstream.URL, "/api/v1/operations"), body: strings.Repeat("x", (1<<20)+1), status: 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			request := browserRequest(method, test.path, test.body, cookie)
			if test.header != "" {
				if test.value == "" {
					request.Header.Del(test.header)
				} else {
					request.Header.Set(test.header, test.value)
				}
			}
			if test.host != "" {
				request.Host = test.host
			}
			response := serveUI(handler, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "" || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "private-password") {
				t.Fatalf("unsafe response: %v %s", response.Header(), response.Body.String())
			}
		})
	}
	if calls.Load() != initialCalls {
		t.Fatal("a rejected browser request reached an upstream API")
	}
	response := serveUI(handler, browserRequest(http.MethodGet, proxyPath(upstream.URL, "/healthz"), "", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unauthenticated health check failed: %d", response.Code)
	}
	response = serveUI(handler, browserRequest(http.MethodGet, proxyPath(upstream.URL, "/api/v1/missions"), "", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatal("private API data was accessible without a session")
	}
	badLogin := strings.Replace(loginBody(upstream.URL), "private-password", "wrong-password", 1)
	response = serveUI(handler, browserRequest(http.MethodPost, "/ui/connections", badLogin, cookie))
	if response.Code != http.StatusUnauthorized || response.Header().Get("Set-Cookie") != "" {
		t.Fatal("rejected credentials must not replace the existing session")
	}
	assertConnections(t, handler, cookie, 1)
}

func TestBrowserSessionRedirectsAndSecureCookie(t *testing.T) {
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Store(true) }))
	defer destination.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/missions" {
			io.WriteString(w, "{\"items\":[]}")
			return
		}
		w.Header().Set("Set-Cookie", "upstream=must-not-be-saved")
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	handler, err := connections.NewHandler(connections.Config{PublicOrigin: "https://admin.example", APIUpstreams: localMappings(upstream.URL)})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	request := browserRequest(http.MethodPost, "/ui/connections", loginBody(upstream.URL), nil)
	request.Header.Set("Origin", "https://admin.example")
	response := serveUI(handler, request)
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 || !response.Result().Cookies()[0].Secure {
		t.Fatalf("reverse proxy did not issue secure cookie: %d %v", response.Code, response.Header())
	}
	cookie := response.Result().Cookies()[0]
	for _, path := range []string{"/api/v1/redirect", "/aboutz"} {
		request = browserRequest(http.MethodGet, proxyPath(upstream.URL, path), "", cookie)
		request.Header.Set("Origin", "https://admin.example")
		response = serveUI(handler, request)
		if response.Code != http.StatusBadGateway || redirected.Load() || response.Header().Get("Location") != "" || response.Header().Get("Set-Cookie") != "" {
			t.Fatalf("upstream redirect escaped proxy: %d %v", response.Code, response.Header())
		}
	}
	request = browserRequest(http.MethodPost, "/ui/connections", loginBody(upstream.URL+"/redirect"), cookie)
	request.Header.Set("Origin", "https://admin.example")
	response = serveUI(handler, request)
	if response.Code != http.StatusBadGateway || redirected.Load() || response.Header().Get("Set-Cookie") != "" {
		t.Fatal("login must reject an API redirect instead of forwarding credentials")
	}
}

func TestOldUnauthorizedReplyPreservesNewLogin(t *testing.T) {
	dispatched := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/actions" {
			close(dispatched)
			<-release
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, "{\"items\":[]}")
	}))
	defer upstream.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	handler, err := connections.NewHandler(connections.Config{APIUpstreams: localMappings(upstream.URL)})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	old := login(t, handler, upstream.URL, nil)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		finished <- serveUI(handler, browserRequest(http.MethodGet, proxyPath(upstream.URL, "/api/v1/actions"), "", old))
	}()
	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("old request was not dispatched")
	}
	fresh := login(t, handler, upstream.URL, old)
	close(release)
	select {
	case response := <-finished:
		if response.Code != http.StatusUnauthorized || response.Header().Get("Set-Cookie") != "" {
			t.Fatal("old unauthorized response must not clear the new cookie")
		}
	case <-time.After(time.Second):
		t.Fatal("old request did not finish")
	}
	assertConnections(t, handler, fresh, 1)
}

func browserRequest(method, path, body string, cookie *http.Cookie) *http.Request {
	request := httptest.NewRequest(method, "http://localhost:4275"+path, strings.NewReader(body))
	request.Header.Set("X-Commander-UI", "1")
	request.Header.Set("Origin", "http://localhost:4275")
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	return request
}

func serveUI(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func login(t *testing.T, handler http.Handler, target string, cookie *http.Cookie) *http.Cookie {
	t.Helper()
	return loginAs(t, handler, target, cookie, "operator", "private-password")
}

func loginAs(t *testing.T, handler http.Handler, target string, cookie *http.Cookie, username, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"url": target, "username": username, "password": password})
	response := serveUI(handler, browserRequest(http.MethodPost, "/ui/connections", string(body), cookie))
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("login failed: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), password) {
		t.Fatal("login response exposed the password")
	}
	return response.Result().Cookies()[0]
}

func loginBody(target string) string {
	body, _ := json.Marshal(map[string]string{"url": target, "username": "operator", "password": "private-password"})
	return string(body)
}

func assertConnections(t *testing.T, handler http.Handler, cookie *http.Cookie, count int) {
	t.Helper()
	response := serveUI(handler, browserRequest(http.MethodGet, "/ui/connections", "", cookie))
	var body struct{ Items []connections.Connection }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Items) != count || strings.Contains(response.Body.String(), "private-password") {
		t.Fatalf("connections=%s status=%d want=%d", response.Body.String(), response.Code, count)
	}
}

func proxyPath(target, path string) string {
	return "/ui/proxy?" + url.Values{"url": {target}, "path": {path}}.Encode()
}

func localMappings(addresses ...string) string {
	entries := make([]string, 0, len(addresses))
	for _, address := range addresses {
		host := strings.TrimPrefix(address, "http://")
		entries = append(entries, host+"="+host)
	}
	return strings.Join(entries, ",")
}
