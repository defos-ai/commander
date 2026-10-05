package api_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/adapters/api/cli"
)

const adminOrigin = "https://admin.example"

func TestCORSApprovedOriginsKeepAuthenticationAndExposeResponses(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_ALLOWED_ORIGINS", "")
	server := startAPI(t, append(fixture.localArgs(), "--allowed-origins="+adminOrigin+",http://localhost:4275"))
	for _, origin := range []string{adminOrigin, "http://localhost:4275"} {
		for _, tc := range []struct {
			method, path, code string
			status             int
			authenticated      bool
		}{
			{http.MethodGet, "/api/v1/missions", "", 200, true},
			{http.MethodGet, "/api/v1/missions", "authentication_required", 401, false},
			{http.MethodGet, "/api/v1/missing", "", 404, true},
			{http.MethodDelete, "/api/v1/missions", "", 405, true},
			{http.MethodPost, "/api/v1/operations", "invalid_request", 400, true},
		} {
			result := server.request(t, tc.method, tc.path, corsOrigin(origin), func(r *http.Request) {
				if tc.authenticated {
					authenticate(r)
				}
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			})
			if result.status != tc.status {
				t.Fatalf("%s %s from %s: status=%d body=%s", tc.method, tc.path, origin, result.status, result.body)
			}
			if tc.code != "" {
				assertAPIError(t, result, tc.status, tc.code)
			}
			assertCORSActual(t, result, origin)
			if tc.status == http.StatusUnauthorized && result.header.Get("WWW-Authenticate") == "" {
				t.Fatal("readable authentication failure lost its Basic challenge")
			}
		}
	}
	for _, fetchSite := range []string{"same-site", "cross-site"} {
		result := server.request(t, http.MethodGet, "/api/v1/actions", authenticate, corsOrigin(adminOrigin), func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", fetchSite) })
		if result.status != http.StatusOK {
			t.Fatalf("approved Origin with %s metadata was rejected: %+v", fetchSite, result)
		}
	}
	wrongPassword := server.request(t, http.MethodGet, "/api/v1/missions", corsOrigin(adminOrigin), func(r *http.Request) { r.SetBasicAuth("api-fixture-user", "wrong-password") })
	assertAPIError(t, wrongPassword, http.StatusUnauthorized, "authentication_required")
	assertCORSActual(t, wrongPassword, adminOrigin)
}

func TestCORSPreflightAuthorizesMethodsAndHeadersWithoutCredentials(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_ALLOWED_ORIGINS", adminOrigin)
	server := startAPI(t, fixture.localArgs())
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		for _, headers := range []string{"", "Authorization", "authorization, CONTENT-TYPE, Accept"} {
			result := server.request(t, http.MethodOptions, "/api/v1/operations", corsPreflight(adminOrigin, method, headers))
			if result.status != http.StatusNoContent || result.body != "" || result.header.Get("WWW-Authenticate") != "" {
				t.Fatalf("preflight must be empty and unauthenticated: %+v", result)
			}
			assertCORSOrigin(t, result, adminOrigin)
			assertCORSTokens(t, result.header, "Access-Control-Allow-Methods", "GET", "HEAD", "POST")
			assertCORSTokens(t, result.header, "Access-Control-Allow-Headers", "Authorization", "Content-Type", "Accept")
			assertCORSVary(t, result.header, "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers")
		}
	}
	for _, tc := range []struct {
		name, method, headers, code string
		status                      int
		repeatMethod                bool
	}{
		{"unsupported method", "DELETE", "", "cors_method_not_allowed", 403, false},
		{"unsupported header", "POST", "Authorization, X-Private", "cors_headers_not_allowed", 403, false},
		{"malformed method", "GET, POST", "", "invalid_cors_preflight", 400, false},
		{"invalid method token", "GET/POST", "", "invalid_cors_preflight", 400, false},
		{"empty method", "", "", "invalid_cors_preflight", 400, false},
		{"repeated method", "GET", "", "invalid_cors_preflight", 400, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := server.request(t, http.MethodOptions, "/api/v1/missions", corsPreflight(adminOrigin, tc.method, tc.headers), func(r *http.Request) {
				if tc.repeatMethod {
					r.Header.Add("Access-Control-Request-Method", tc.method)
				}
			})
			assertAPIError(t, result, tc.status, tc.code)
			assertCORSOrigin(t, result, adminOrigin)
			assertCORSVary(t, result.header, "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers")
			if result.header.Get("WWW-Authenticate") != "" || result.header.Get("Access-Control-Allow-Methods") != "" {
				t.Fatalf("rejected preflight issued a challenge or grant: %v", result.header)
			}
		})
	}
}

func TestCORSRejectsUnapprovedAndMalformedOrigins(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_ALLOWED_ORIGINS", adminOrigin)
	server := startAPI(t, fixture.localArgs())
	for _, origin := range []string{
		"https://attacker.invalid", "https://admin.example.attacker.invalid", "http://admin.example", "https://admin.example:444",
		"null", "*", adminOrigin + "/", adminOrigin + "?query", adminOrigin + "#fragment", "https://user@admin.example",
		adminOrigin + " https://other.example", adminOrigin + ",https://other.example",
	} {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			result := server.request(t, method, "/api/v1/missions", authenticate, corsOrigin(origin), func(r *http.Request) {
				if method == http.MethodOptions {
					r.Header.Set("Access-Control-Request-Method", "GET")
				}
			})
			assertAPIError(t, result, http.StatusForbidden, "cross_origin_request")
			assertCORSNotGranted(t, result)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		result := server.request(t, method, "/api/v1/missions", authenticate, corsPreflight(adminOrigin, "GET", ""), func(r *http.Request) { r.Header.Add("Origin", adminOrigin) })
		assertAPIError(t, result, http.StatusForbidden, "cross_origin_request")
		assertCORSNotGranted(t, result)
	}
	for _, fetchSite := range []string{"same-site", "cross-site"} {
		result := server.request(t, http.MethodGet, "/api/v1/missions", authenticate, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", fetchSite) })
		assertAPIError(t, result, http.StatusForbidden, "cross_origin_request")
		assertCORSNotGranted(t, result)
	}
	for _, values := range [][]string{{"invalid"}, {"same-site, cross-site"}, {"same-site", "cross-site"}} {
		result := server.request(t, http.MethodGet, "/api/v1/missions", authenticate, corsOrigin(adminOrigin), func(r *http.Request) {
			r.Header["Sec-Fetch-Site"] = values
		})
		assertAPIError(t, result, http.StatusForbidden, "cross_origin_request")
		assertCORSNotGranted(t, result)
	}
}

func TestCORSNonPreflightOptionsKeepAuthenticationAndRouting(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_ALLOWED_ORIGINS", adminOrigin)
	server := startAPI(t, fixture.localArgs())
	for _, configure := range []func(*http.Request){
		func(*http.Request) {},
		corsOrigin(adminOrigin),
		func(r *http.Request) { r.Header.Set("Access-Control-Request-Method", "GET") },
	} {
		unauthorized := server.request(t, http.MethodOptions, "/api/v1/missions", configure)
		assertAPIError(t, unauthorized, http.StatusUnauthorized, "authentication_required")
		authenticated := server.request(t, http.MethodOptions, "/api/v1/missions", configure, authenticate)
		if authenticated.status != http.StatusMethodNotAllowed || authenticated.header.Get("Access-Control-Allow-Methods") != "" {
			t.Fatalf("ordinary OPTIONS bypassed routing: %+v", authenticated)
		}
	}
}

func TestCORSHealthBypassAndProtectedProxyValidation(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_ALLOWED_ORIGINS", adminOrigin)
	server := startAPI(t, append(fixture.localArgs(), "--allowed-hosts=api.example", "--trusted-proxies=127.0.0.1", "--proxy-headers=forwarded"))
	for _, path := range []string{"/healthz", "/readyz"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, origin := range []string{adminOrigin, "https://unapproved.example", "null"} {
				result := server.request(t, method, path, corsOrigin(origin), func(r *http.Request) {
					r.Host = "internal-load-balancer.invalid"
					r.Header.Set("Forwarded", "malformed-forwarding")
				})
				if result.status != http.StatusOK {
					t.Fatalf("public probe lost its host/proxy bypass: %+v", result)
				}
				if origin == adminOrigin {
					assertCORSActual(t, result, origin)
				} else {
					assertCORSNotGranted(t, result)
				}
			}
		}
	}
	for _, path := range []string{"/api/v1/missions", "/healthz"} {
		approved := server.request(t, http.MethodOptions, path, corsPreflight(adminOrigin, "GET", "Authorization"), func(r *http.Request) {
			r.Host = "internal.invalid"
			r.Header.Set("Forwarded", "host=api.example;proto=https")
		})
		if approved.status != http.StatusNoContent || approved.header.Get("Strict-Transport-Security") == "" {
			t.Fatalf("trusted proxy preflight did not use effective origin: %+v", approved)
		}
		assertCORSOrigin(t, approved, adminOrigin)
		for _, tc := range []struct {
			forwarded, code string
			status          int
		}{
			{"host=blocked.example;proto=https", "host_not_allowed", 403},
			{"malformed-forwarding", "invalid_request_origin", 400},
		} {
			result := server.request(t, http.MethodOptions, path, corsPreflight(adminOrigin, "GET", "Authorization"), func(r *http.Request) {
				r.Host = "api.example"
				r.Header.Set("Forwarded", tc.forwarded)
			})
			assertAPIError(t, result, tc.status, tc.code)
			assertCORSActual(t, result, adminOrigin)
			if result.header.Get("Access-Control-Allow-Methods") != "" {
				t.Fatal("proxy or host rejection granted preflight access")
			}
		}
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "database", "missions.json"), []byte("invalid fixture data"), 0600); err != nil {
		t.Fatal(err)
	}
	unready := server.request(t, http.MethodGet, "/readyz", corsOrigin(adminOrigin))
	if unready.status != http.StatusServiceUnavailable {
		t.Fatalf("unreadable database should report unavailable: %+v", unready)
	}
	assertCORSActual(t, unready, adminOrigin)

	t.Run("untrusted proxy", func(t *testing.T) {
		fixture := newFixture(t)
		t.Setenv("API_ALLOWED_ORIGINS", adminOrigin)
		server := startAPI(t, append(fixture.localArgs(), "--allowed-hosts=api.example", "--trusted-proxies=192.0.2.0/24", "--proxy-headers=forwarded"))
		result := server.request(t, http.MethodOptions, "/api/v1/missions", corsPreflight(adminOrigin, "GET", ""), func(r *http.Request) {
			r.Host = "internal.invalid"
			r.Header.Set("Forwarded", "host=api.example;proto=https")
		})
		assertAPIError(t, result, http.StatusForbidden, "host_not_allowed")
		assertCORSActual(t, result, adminOrigin)
	})
}

func TestCORSOriginConfigurationPrecedenceAndDefault(t *testing.T) {
	for _, tc := range []struct {
		name, environment, flag string
		allowed, denied         string
	}{
		{"default localhost UI", "", "", "http://localhost:4275", adminOrigin},
		{"default loopback UI", "", "", "http://127.0.0.1:4275", "http://localhost:4276"},
		{"environment replaces defaults", adminOrigin, "", adminOrigin, "http://localhost:4275"},
		{"normalized configuration", "https://ADMIN.example:443", "", adminOrigin, "https://admin.example:444"},
		{"echoes request origin", adminOrigin, "", "https://admin.example:443", "http://admin.example"},
		{"IPv6 origin", "http://[::1]:4275", "", "http://[::1]:4275", "http://[::1]:4274"},
		{"flag overrides environment", "https://environment.example", "--allowed-origins=" + adminOrigin, adminOrigin, "https://environment.example"},
		{"empty flag disables", adminOrigin, "--allowed-origins=", "", adminOrigin},
		{"empty flag disables defaults", "", "--allowed-origins=", "", "http://localhost:4275"},
		{"flag overrides invalid environment", "null", "--allowed-origins=" + adminOrigin, adminOrigin, "https://other.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFixture(t)
			t.Setenv("API_ALLOWED_ORIGINS", tc.environment)
			args := fixture.localArgs()
			if tc.flag != "" {
				args = append(args, tc.flag)
			}
			server := startAPI(t, args)
			if tc.allowed != "" {
				result := server.request(t, http.MethodGet, "/api/v1/missions", authenticate, corsOrigin(tc.allowed))
				if result.status != http.StatusOK {
					t.Fatalf("allowed origin was rejected: %+v", result)
				}
				assertCORSActual(t, result, tc.allowed)
				preflight := server.request(t, http.MethodOptions, "/api/v1/missions", corsPreflight(tc.allowed, "GET", "Authorization"))
				if preflight.status != http.StatusNoContent {
					t.Fatalf("allowed origin preflight was rejected: %+v", preflight)
				}
			}
			assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/missions", authenticate, corsOrigin(tc.denied)), http.StatusForbidden, "cross_origin_request")
			sameOrigin := server.request(t, http.MethodGet, "/api/v1/missions", authenticate, corsOrigin(server.url))
			if sameOrigin.status != http.StatusOK {
				t.Fatalf("same-origin access changed: %+v", sameOrigin)
			}
		})
	}
}

func TestCORSRejectsInvalidConfiguredOriginsAtStartup(t *testing.T) {
	for _, origin := range []string{"*", "https://*.example", "null", "ftp://admin.example", adminOrigin + "/", adminOrigin + "/path", adminOrigin + "?query", adminOrigin + "#fragment", "https://user:private-origin-password@admin.example", "https://admin.example:0", "https://admin.example:65536", "https://admin.example:bad", "https://admin.example:", adminOrigin + ","} {
		t.Run(origin, func(t *testing.T) {
			fixture := newFixture(t)
			t.Setenv("API_ALLOWED_ORIGINS", origin)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var logs bytes.Buffer
			err := cli.Run(ctx, fixture.localArgs(), &logs)
			if err == nil || ctx.Err() != nil {
				t.Fatalf("invalid origin did not fail startup promptly: %v", err)
			}
			if !strings.Contains(err.Error(), "allowed-origins") && !strings.Contains(err.Error(), "API_ALLOWED_ORIGINS") {
				t.Fatalf("configuration error does not identify allowed origins: %v", err)
			}
			if strings.Contains(err.Error()+logs.String(), "private-origin-password") || strings.Contains(err.Error()+logs.String(), "api-fixture-password") {
				t.Fatal("startup error exposed credentials")
			}
		})
	}
}

func corsOrigin(origin string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Origin", origin) }
}

func corsPreflight(origin, method, headers string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", method)
		if headers != "" {
			r.Header.Set("Access-Control-Request-Headers", headers)
		}
	}
}

func assertCORSOrigin(t *testing.T, result response, origin string) {
	t.Helper()
	if values := result.header.Values("Access-Control-Allow-Origin"); len(values) != 1 || values[0] != origin {
		t.Fatalf("CORS did not echo one approved origin: %v", result.header)
	}
	if result.header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("CORS must not allow browser-managed credentials")
	}
	assertCORSVary(t, result.header, "Origin")
}

func assertCORSActual(t *testing.T, result response, origin string) {
	t.Helper()
	assertCORSOrigin(t, result, origin)
	assertCORSTokens(t, result.header, "Access-Control-Expose-Headers", "Location", "Content-Disposition", "WWW-Authenticate")
}

func assertCORSNotGranted(t *testing.T, result response) {
	t.Helper()
	if result.header.Get("Access-Control-Allow-Origin") != "" || result.header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("unapproved origin received CORS access: %v", result.header)
	}
}

func corsTokens(header http.Header, name string) map[string]bool {
	result := map[string]bool{}
	for _, line := range header.Values(name) {
		for _, value := range strings.Split(line, ",") {
			if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
				result[value] = true
			}
		}
	}
	return result
}

func assertCORSTokens(t *testing.T, header http.Header, name string, values ...string) {
	t.Helper()
	want := map[string]bool{}
	for _, value := range values {
		want[strings.ToLower(value)] = true
	}
	if got := corsTokens(header, name); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s=%v, want %v", name, got, want)
	}
}

func assertCORSVary(t *testing.T, header http.Header, names ...string) {
	t.Helper()
	values := corsTokens(header, "Vary")
	for _, name := range names {
		if !values[strings.ToLower(name)] {
			t.Errorf("Vary is missing %s: %v", name, header.Values("Vary"))
		}
	}
}
