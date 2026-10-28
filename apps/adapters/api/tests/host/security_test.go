package api_test

import (
	"net/http"
	"strings"
	"testing"

	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestBasicAuthenticationRequiresExactCredentials(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	const username, password = "api-fixture-user", "api-fixture-password"
	for _, test := range []struct {
		name, username, password string
	}{
		{"wrong username", "api-fixture-useX", password},
		{"short username", "api-fixture-use", password},
		{"long username", username + "X", password},
		{"empty username", "", password},
		{"wrong password", username, "api-fixture-passworX"},
		{"short password", username, "api-fixture-passwor"},
		{"long password", username, password + "X"},
		{"empty password", username, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := server.Request(t, http.MethodGet, "/api/v1/missions", func(request *http.Request) {
				request.SetBasicAuth(test.username, test.password)
			})
			assertAPIError(t, result, http.StatusUnauthorized, "authentication_required")
			if !strings.HasPrefix(result.Header.Get("WWW-Authenticate"), "Basic ") {
				t.Error("authentication failure lost its Basic challenge")
			}
		})
	}
	duplicate := server.Request(t, http.MethodGet, "/api/v1/missions", hostfixtures.Authenticate, func(request *http.Request) {
		request.Header.Add("Authorization", request.Header.Get("Authorization"))
	})
	assertAPIError(t, duplicate, http.StatusUnauthorized, "authentication_required")
	if result := server.Request(t, http.MethodGet, "/api/v1/missions", hostfixtures.Authenticate); result.Status != http.StatusOK {
		t.Fatalf("configured credentials were rejected: status=%d", result.Status)
	}
}

func TestPublicHealthProbesBypassHostAndProxyChecks(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, append(fixture.LocalArgs(), "--allowed-hosts=api.example", "--trusted-proxies=127.0.0.1", "--proxy-headers=forwarded"))
	for _, path := range []string{"/healthz", "/readyz"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+" "+path, func(t *testing.T) {
				result := server.Request(t, method, path, func(r *http.Request) {
					r.Host = "internal-load-balancer.invalid"
					r.Header.Set("Forwarded", "malformed-forwarding")
					r.Header.Set("Origin", "https://other.example")
				})

				if result.Status != http.StatusOK {
					t.Fatalf("public probe returned %d: %s", result.Status, result.Body)
				}
				assertSecurityHeaders(t, result, false)
			})
		}
	}
}

func TestHostAndOriginValidation(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	for _, test := range []struct {
		name, method, host, origin, fetchSite string
		duplicateOrigin                       bool
		status                                int
	}{
		{name: "native client", status: http.StatusNotFound},
		{name: "same origin", origin: server.URL, status: http.StatusNotFound},
		{name: "host is case insensitive", host: "LOCALHOST", origin: "http://localhost", status: http.StatusNotFound},
		{name: "loopback IPv6", host: "[::1]", origin: "http://[::1]", status: http.StatusNotFound},
		{name: "default port equivalence", host: "localhost:80", origin: "http://localhost", status: http.StatusNotFound},
		{name: "trailing dot is a different origin", host: "localhost", origin: "http://localhost.", status: http.StatusForbidden},
		{name: "IPv4 mapped IPv6 is a different origin", host: "127.0.0.1", origin: "http://[::ffff:127.0.0.1]", status: http.StatusForbidden},
		{name: "unknown host", host: "attacker.invalid", status: http.StatusForbidden},
		{name: "host suffix is not allowed", host: "localhost.attacker.invalid", status: http.StatusForbidden},
		{name: "different origin hostname", origin: "http://attacker.invalid", status: http.StatusForbidden},
		{name: "different origin scheme", origin: strings.Replace(server.URL, "http:", "https:", 1), status: http.StatusForbidden},
		{name: "unconfigured origin port", host: "localhost:4274", origin: "http://localhost:4276", status: http.StatusForbidden},
		{name: "opaque origin", origin: "null", status: http.StatusForbidden},
		{name: "cross site navigation", fetchSite: "cross-site", status: http.StatusForbidden},
		{name: "same site is not same origin", fetchSite: "same-site", status: http.StatusForbidden},
		{name: "same origin navigation", fetchSite: "same-origin", status: http.StatusNotFound},
		{name: "native mutation", method: http.MethodPost, status: http.StatusNotFound},
		{name: "cross origin mutation", method: http.MethodPost, origin: "https://attacker.invalid", status: http.StatusForbidden},
		{name: "origin mismatch despite same origin metadata", origin: "https://attacker.invalid", fetchSite: "same-origin", status: http.StatusForbidden},
		{name: "duplicate origin", origin: server.URL, duplicateOrigin: true, status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			result := server.Request(t, method, "/api/v1/nonexistent", hostfixtures.Authenticate, func(r *http.Request) {
				if test.host != "" {
					r.Host = test.host
				}
				if test.origin != "" {
					r.Header.Set("Origin", test.origin)
					if test.duplicateOrigin {
						r.Header.Add("Origin", test.origin)
					}
				}
				if test.fetchSite != "" {
					r.Header.Set("Sec-Fetch-Site", test.fetchSite)
				}
			})

			if result.Status != test.status {
				t.Fatalf("status=%d, want %d: %s", result.Status, test.status, result.Body)
			}
			assertSecurityHeaders(t, result, false)
		})
	}
	for _, path := range []string{"/healthz/", "/readyz/private"} {
		if result := server.Request(t, http.MethodGet, path); result.Status != http.StatusUnauthorized {
			t.Errorf("non-probe path %s bypassed authentication: status=%d", path, result.Status)
		}
	}
}

func TestReverseProxyTrustAndHeaderModes(t *testing.T) {
	for _, test := range []struct {
		name, mode, trusted, host string
		headers                   http.Header
		status                    int
		https                     bool
	}{
		{name: "forwarding disabled by default", host: "internal.invalid", headers: http.Header{"Forwarded": {"host=api.example;proto=https"}}, status: http.StatusForbidden},
		{name: "untrusted peer cannot override host", mode: "forwarded", trusted: "192.0.2.0/24", host: "internal.invalid", headers: http.Header{"Forwarded": {"host=api.example;proto=https"}}, status: http.StatusForbidden},
		{name: "untrusted malformed forwarding is ignored", mode: "forwarded", trusted: "192.0.2.0/24", host: "api.example", headers: http.Header{"Forwarded": {"malformed"}}, status: http.StatusNotFound},
		{name: "trusted RFC forwarded", mode: "forwarded", trusted: "127.0.0.0/8", host: "internal.invalid", headers: http.Header{"Forwarded": {`for="[2001:db8::1]";host="api.example:443";proto=https`}, "Origin": {"https://api.example"}}, status: http.StatusNotFound, https: true},
		{name: "unselected X headers are ignored", mode: "forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"X-Forwarded-Host": {"attacker.invalid"}, "X-Forwarded-Proto": {"https"}}, status: http.StatusNotFound},
		{name: "trusted X forwarded", mode: "x-forwarded", trusted: "127.0.0.1/32", host: "internal.invalid", headers: http.Header{"X-Forwarded-Host": {"api.example:443"}, "X-Forwarded-Proto": {"https"}, "Origin": {"https://api.example"}, "Forwarded": {"malformed"}}, status: http.StatusNotFound, https: true},
		{name: "proto only preserves original host", mode: "x-forwarded-proto", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"X-Forwarded-Host": {"attacker.invalid"}, "X-Forwarded-Proto": {"https"}}, status: http.StatusNotFound, https: true},
		{name: "trusted missing headers use original host", mode: "x-forwarded", trusted: "127.0.0.1", host: "api.example", status: http.StatusNotFound},
		{name: "forwarded host must be allowed", mode: "forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"Forwarded": {"host=attacker.invalid;proto=https"}}, status: http.StatusForbidden, https: true},
		{name: "forwarded origin must match", mode: "forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"Forwarded": {"host=api.example;proto=https"}, "Origin": {"http://api.example"}}, status: http.StatusForbidden, https: true},
		{name: "forwarded chain rejected", mode: "forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"Forwarded": {"host=api.example;proto=https,host=attacker.invalid"}}, status: http.StatusBadRequest},
		{name: "forwarded repeated header rejected", mode: "forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"Forwarded": {"host=api.example;proto=https", "host=api.example;proto=https"}}, status: http.StatusBadRequest},
		{name: "forwarded repeated parameter rejected", mode: "forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"Forwarded": {"host=api.example;host=attacker.invalid;proto=https"}}, status: http.StatusBadRequest},
		{name: "forwarded identical repeated parameter rejected", mode: "forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"Forwarded": {"host=api.example;host=api.example;proto=https"}}, status: http.StatusBadRequest},
		{name: "forwarded MIME parameter encoding rejected", mode: "forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"Forwarded": {"host*=UTF-8''api.example;proto=https"}}, status: http.StatusBadRequest},
		{name: "invalid forwarded scheme", mode: "forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"Forwarded": {"host=api.example;proto=ftp"}}, status: http.StatusBadRequest},
		{name: "invalid X host list", mode: "x-forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"X-Forwarded-Host": {"api.example,attacker.invalid"}}, status: http.StatusBadRequest},
		{name: "invalid repeated X proto", mode: "x-forwarded", trusted: "127.0.0.1", host: "api.example", headers: http.Header{"X-Forwarded-Proto": {"https", "https"}}, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := hostfixtures.New(t)
			args := append(fixture.LocalArgs(), "--allowed-hosts=api.example")
			if test.mode != "" {
				args = append(args, "--proxy-headers="+test.mode, "--trusted-proxies="+test.trusted)
			}
			server := hostfixtures.Start(t, args)

			result := server.Request(t, http.MethodGet, "/api/v1/nonexistent", hostfixtures.Authenticate, func(r *http.Request) {
				r.Host = test.host
				r.Header = test.headers.Clone()
				if r.Header == nil {
					r.Header = make(http.Header)
				}
				hostfixtures.Authenticate(r)
			})

			if result.Status != test.status {
				t.Fatalf("status=%d, want %d: %s", result.Status, test.status, result.Body)
			}
			assertSecurityHeaders(t, result, test.https)
		})
	}
}

func assertSecurityHeaders(t *testing.T, result hostfixtures.Response, https bool) {
	t.Helper()
	for name, want := range map[string]string{
		"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
	} {
		if got := result.Header.Get(name); got != want {
			t.Errorf("%s=%q, want %q", name, got, want)
		}
	}
	if policy := result.Header.Get("Content-Security-Policy"); !strings.Contains(policy, "default-src 'none'") || !strings.Contains(policy, "frame-ancestors 'none'") {
		t.Errorf("missing restrictive CSP: %q", policy)
	}
	if got := result.Header.Get("Strict-Transport-Security"); (got != "") != https {
		t.Errorf("HSTS=%q, effective HTTPS=%v", got, https)
	}
	if result.Header.Get("Access-Control-Allow-Origin") != "" || result.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("API unexpectedly allows cross-origin requests")
	}
}
