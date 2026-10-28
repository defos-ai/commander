package fixtures

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func BrowserRequest(method, path, body string, cookie *http.Cookie) *http.Request {
	request := httptest.NewRequest(method, "http://localhost:4275"+path, strings.NewReader(body))
	request.Header.Set("X-Commander-UI", "1")
	request.Header.Set("Origin", "http://localhost:4275")
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	return request
}

func ServeUI(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func Login(t *testing.T, handler http.Handler, target string, cookie *http.Cookie) *http.Cookie {
	t.Helper()
	return LoginAs(t, handler, target, cookie, "operator", "private-password")
}

func LoginAs(t *testing.T, handler http.Handler, target string, cookie *http.Cookie, username, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"url": target, "username": username, "password": password})
	response := ServeUI(handler, BrowserRequest(http.MethodPost, "/ui/connections", string(body), cookie))
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("login failed: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), password) {
		t.Fatal("login response exposed the password")
	}
	return response.Result().Cookies()[0]
}

func LoginBody(target string) string {
	body, _ := json.Marshal(map[string]string{"url": target, "username": "operator", "password": "private-password"})
	return string(body)
}

func ProxyPath(target, path string) string {
	return "/ui/proxy?" + url.Values{"url": {target}, "path": {path}}.Encode()
}

func LocalMappings(addresses ...string) string {
	entries := make([]string, 0, len(addresses))
	for _, address := range addresses {
		host := strings.TrimPrefix(address, "http://")
		entries = append(entries, host+"="+host)
	}
	return strings.Join(entries, ",")
}
