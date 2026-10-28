package api_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestCORSPatchPreflightAndAuthenticatedCatalogUpdates(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	config.Security.AllowedOrigins = []string{adminOrigin}
	entry := hostfixtures.CatalogEntry(hostfixtures.CatalogPackage)
	if err := config.CatalogStore.Create(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	path := catalogPath + "/" + url.PathEscape(entry.Package)

	preflight := hostfixtures.CORSRequest(t.Context(), handler, http.MethodOptions, path, "", corsPreflight(adminOrigin, http.MethodPatch, "Authorization, Content-Type"))

	if preflight.Status != http.StatusNoContent || preflight.Body != "" || preflight.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("PATCH preflight must be empty and unauthenticated: %+v", preflight)
	}
	assertCORSOrigin(t, preflight, adminOrigin)
	assertCORSTokens(t, preflight.Header, "Access-Control-Allow-Methods", "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE")
	assertCORSTokens(t, preflight.Header, "Access-Control-Allow-Headers", "Authorization", "Content-Type", "Accept")
	assertCORSVary(t, preflight.Header, "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers")

	unauthorized := hostfixtures.CORSRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":true}`, corsOrigin(adminOrigin))

	assertAPIError(t, unauthorized, http.StatusUnauthorized, "authentication_required")
	assertCORSActual(t, unauthorized, adminOrigin)
	if unauthorized.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("PATCH authentication failure lost its Basic challenge")
	}

	updated := hostfixtures.CORSRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":true}`, corsOrigin(adminOrigin), func(request *http.Request) {
		request.SetBasicAuth("log-user", "log-password")
		request.Header.Set("Sec-Fetch-Site", "cross-site")
	})

	assertCORSActual(t, updated, adminOrigin)
	saved := decodeResponse[models.CatalogAgentResponse](t, updated)
	if !saved.Trusted || saved.Version != 2 || saved.TrustUpdatedBy != "log-user" {
		t.Fatalf("authenticated cross-origin PATCH lost its trust decision: %+v", saved)
	}
}

func TestCORSPatchKeepsHostOriginAndHeaderChecks(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	config.Security.AllowedOrigins = []string{adminOrigin}
	store := fakes.NewCatalogFailures(config.CatalogStore, "", nil)
	config.CatalogStore = store
	handler := hostfixtures.LogHandler(t, config)
	path := catalogPath + "/" + url.PathEscape(hostfixtures.CatalogPackage)
	for _, test := range []struct {
		name, method, origin, host, requestMethod, headers, fetchSite, password, code string
		status                                                                        int
	}{
		{name: "preflight origin", method: http.MethodOptions, origin: "https://unapproved.example", requestMethod: http.MethodPatch, code: "cross_origin_request", status: http.StatusForbidden},
		{name: "preflight host", method: http.MethodOptions, origin: adminOrigin, host: "blocked.example", requestMethod: http.MethodPatch, code: "host_not_allowed", status: http.StatusForbidden},
		{name: "preflight headers", method: http.MethodOptions, origin: adminOrigin, requestMethod: http.MethodPatch, headers: "Authorization, X-Private", code: "cors_headers_not_allowed", status: http.StatusForbidden},
		{name: "preflight malformed method", method: http.MethodOptions, origin: adminOrigin, requestMethod: "PATCH, GET", code: "invalid_cors_preflight", status: http.StatusBadRequest},
		{name: "preflight unsupported method", method: http.MethodOptions, origin: adminOrigin, requestMethod: http.MethodTrace, code: "cors_method_not_allowed", status: http.StatusForbidden},
		{name: "mutation origin", method: http.MethodPatch, origin: "https://unapproved.example", password: "log-password", code: "cross_origin_request", status: http.StatusForbidden},
		{name: "mutation host", method: http.MethodPatch, origin: adminOrigin, host: "blocked.example", password: "log-password", code: "host_not_allowed", status: http.StatusForbidden},
		{name: "mutation credentials", method: http.MethodPatch, origin: adminOrigin, password: "wrong-password", code: "authentication_required", status: http.StatusUnauthorized},
		{name: "mutation fetch metadata", method: http.MethodPatch, origin: adminOrigin, fetchSite: "invalid", password: "log-password", code: "cross_origin_request", status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {

			result := hostfixtures.CORSRequest(t.Context(), handler, test.method, path, `{"version":1,"trusted":true}`, corsOrigin(test.origin), func(request *http.Request) {
				if test.password != "" {
					request.SetBasicAuth("log-user", test.password)
				}
				if test.host != "" {
					request.Host = test.host
				}
				if test.requestMethod != "" {
					request.Header.Set("Access-Control-Request-Method", test.requestMethod)
				}
				if test.headers != "" {
					request.Header.Set("Access-Control-Request-Headers", test.headers)
				}
				if test.fetchSite != "" {
					request.Header.Set("Sec-Fetch-Site", test.fetchSite)
				}
			})

			assertAPIError(t, result, test.status, test.code)
			if test.origin == adminOrigin && test.fetchSite == "" {
				assertCORSActual(t, result, adminOrigin)
			} else {
				assertCORSNotGranted(t, result)
			}
			if result.Header.Get("Access-Control-Allow-Methods") != "" {
				t.Fatal("rejected PATCH request received a preflight grant")
			}
		})
	}
	if store.Snapshot("get").Attempts != 0 || store.Snapshot("replace").Attempts != 0 {
		t.Fatal("rejected PATCH request reached catalog storage")
	}
}
