package api_test

import (
	"net/http"
	"strings"
	"testing"
)

func authenticate(request *http.Request) {
	request.SetBasicAuth("api-fixture-user", "api-fixture-password")
}

func TestPublicHealthProbesBypassHostAndProxyChecks(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, append(fixture.localArgs(), "--allowed-hosts=api.example", "--trusted-proxies=127.0.0.1", "--proxy-headers=forwarded"))
	for _, path := range []string{"/healthz", "/readyz"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+" "+path, func(t *testing.T) {
				result := server.request(t, method, path, func(r *http.Request) {
					r.Host = "internal-load-balancer.invalid"
					r.Header.Set("Forwarded", "malformed-forwarding")
					r.Header.Set("Origin", "https://other.example")
				})

				if result.status != http.StatusOK {
					t.Fatalf("public probe returned %d: %s", result.status, result.body)
				}
				assertSecurityHeaders(t, result, false)
			})
		}
	}
}

func TestHostAndOriginValidation(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	for _, test := range []struct {
		name, method, host, origin, fetchSite string
		duplicateOrigin                       bool
		status                                int
	}{
		{name: "native client", status: http.StatusNotFound},
		{name: "same origin", origin: server.url, status: http.StatusNotFound},
		{name: "host is case insensitive", host: "LOCALHOST", origin: "http://localhost", status: http.StatusNotFound},
		{name: "loopback IPv6", host: "[::1]", origin: "http://[::1]", status: http.StatusNotFound},
		{name: "default port equivalence", host: "localhost:80", origin: "http://localhost", status: http.StatusNotFound},
		{name: "trailing dot is a different origin", host: "localhost", origin: "http://localhost.", status: http.StatusForbidden},
		{name: "IPv4 mapped IPv6 is a different origin", host: "127.0.0.1", origin: "http://[::ffff:127.0.0.1]", status: http.StatusForbidden},
		{name: "unknown host", host: "attacker.invalid", status: http.StatusForbidden},
		{name: "host suffix is not allowed", host: "localhost.attacker.invalid", status: http.StatusForbidden},
		{name: "different origin hostname", origin: "http://attacker.invalid", status: http.StatusForbidden},
		{name: "different origin scheme", origin: strings.Replace(server.url, "http:", "https:", 1), status: http.StatusForbidden},
		{name: "unconfigured origin port", host: "localhost:4274", origin: "http://localhost:4276", status: http.StatusForbidden},
		{name: "opaque origin", origin: "null", status: http.StatusForbidden},
		{name: "cross site navigation", fetchSite: "cross-site", status: http.StatusForbidden},
		{name: "same site is not same origin", fetchSite: "same-site", status: http.StatusForbidden},
		{name: "same origin navigation", fetchSite: "same-origin", status: http.StatusNotFound},
		{name: "native mutation", method: http.MethodPost, status: http.StatusNotFound},
		{name: "cross origin mutation", method: http.MethodPost, origin: "https://attacker.invalid", status: http.StatusForbidden},
		{name: "origin mismatch despite same origin metadata", origin: "https://attacker.invalid", fetchSite: "same-origin", status: http.StatusForbidden},
		{name: "duplicate origin", origin: server.url, duplicateOrigin: true, status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			result := server.request(t, method, "/api/v1/nonexistent", authenticate, func(r *http.Request) {
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

			if result.status != test.status {
				t.Fatalf("status=%d, want %d: %s", result.status, test.status, result.body)
			}
			assertSecurityHeaders(t, result, false)
		})
	}
	for _, path := range []string{"/healthz/", "/readyz/private"} {
		if result := server.request(t, http.MethodGet, path); result.status != http.StatusUnauthorized {
			t.Errorf("non-probe path %s bypassed authentication: status=%d", path, result.status)
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
			fixture := newFixture(t)
			args := append(fixture.localArgs(), "--allowed-hosts=api.example")
			if test.mode != "" {
				args = append(args, "--proxy-headers="+test.mode, "--trusted-proxies="+test.trusted)
			}
			server := startAPI(t, args)

			result := server.request(t, http.MethodGet, "/api/v1/nonexistent", authenticate, func(r *http.Request) {
				r.Host = test.host
				r.Header = test.headers.Clone()
				if r.Header == nil {
					r.Header = make(http.Header)
				}
				authenticate(r)
			})

			if result.status != test.status {
				t.Fatalf("status=%d, want %d: %s", result.status, test.status, result.body)
			}
			assertSecurityHeaders(t, result, test.https)
		})
	}
}

func assertSecurityHeaders(t *testing.T, result response, https bool) {
	t.Helper()
	for name, want := range map[string]string{
		"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
	} {
		if got := result.header.Get(name); got != want {
			t.Errorf("%s=%q, want %q", name, got, want)
		}
	}
	if policy := result.header.Get("Content-Security-Policy"); !strings.Contains(policy, "default-src 'none'") || !strings.Contains(policy, "frame-ancestors 'none'") {
		t.Errorf("missing restrictive CSP: %q", policy)
	}
	if got := result.header.Get("Strict-Transport-Security"); (got != "") != https {
		t.Errorf("HSTS=%q, effective HTTPS=%v", got, https)
	}
	if result.header.Get("Access-Control-Allow-Origin") != "" || result.header.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("API unexpectedly allows cross-origin requests")
	}
}
