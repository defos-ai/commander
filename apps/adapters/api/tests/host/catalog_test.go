package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/agents"
)

const catalogPath = "/api/v1/catalog-agents"

func TestCatalogCreatesUntrustedPackagesAndReadsSavedEntries(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	handler := hostfixtures.LogHandler(t, config)
	before := time.Now()

	empty := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodGet, catalogPath, "", true)

	if items := decodeResponse[models.ListResponse[models.CatalogAgentResponse]](t, empty).Items; items == nil || len(items) != 0 {
		t.Fatalf("empty catalog must return an empty array: %s", empty.Body)
	}
	var created models.CatalogAgentResponse
	for _, value := range []string{hostfixtures.CatalogPackage, strings.Replace(hostfixtures.CatalogPackage, "@v1.2.3", "@stable", 1)} {
		body := hostfixtures.CatalogCreateBody(value)

		result := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPost, catalogPath, body, true, "application/json; charset=utf-8")

		entry := decodeResponse[models.CatalogAgentResponse](t, result, http.StatusCreated)
		if entry.Package != value || entry.Name != "custom-agent" || entry.Description != "A custom worker" || entry.Visibility != "private" ||
			entry.Trusted || entry.Version != 1 || !entry.CreatedAt.Equal(entry.UpdatedAt) || entry.CreatedAt.Before(before) || entry.CreatedAt.After(time.Now()) ||
			entry.TrustUpdatedBy != "" || !entry.TrustUpdatedAt.IsZero() {
			t.Fatalf("creation did not assign safe defaults: %+v", entry)
		}
		if result.Header.Get("Location") != catalogPath+"/"+url.PathEscape(value) || result.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("creation lost its escaped location or cache policy: %v", result.Header)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(result.Body), &fields) != nil || fields["trust_updated_by"] != nil || fields["trust_updated_at"] != nil {
			t.Fatal("creation fabricated a trust decision")
		}

		duplicate := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPost, catalogPath, body, true)

		assertAPIError(t, duplicate, http.StatusConflict, "catalog_entry_exists")
		if value == hostfixtures.CatalogPackage {
			created = entry
		}
	}
	reopened := hostfixtures.LogHandler(t, config)

	get := hostfixtures.CatalogRequest(t.Context(), reopened, http.MethodGet, catalogPath+"/"+url.PathEscape(created.Package), "", true)
	list := hostfixtures.CatalogRequest(t.Context(), reopened, http.MethodGet, catalogPath, "", true)

	if saved := decodeResponse[models.CatalogAgentResponse](t, get); saved != created {
		t.Fatalf("saved package changed across API hosts: got %+v, want %+v", saved, created)
	}
	items := decodeResponse[models.ListResponse[models.CatalogAgentResponse]](t, list).Items
	if len(items) != 2 || items[0].Package >= items[1].Package {
		t.Fatalf("catalog must distinguish and sort full selectors: %+v", items)
	}
	if config.Operations.(*fakes.FailedStart).Snapshot().Attempts != 0 {
		t.Fatal("catalog access contacted the controller")
	}
}

func TestCatalogTrustDecisionsPreserveIdentityAndRejectStaleRevocations(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	entry := hostfixtures.CatalogEntry(hostfixtures.CatalogPackage)
	if err := config.CatalogStore.Create(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	path := catalogPath + "/" + url.PathEscape(entry.Package)
	before := time.Now()

	result := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":true}`, true)

	granted := decodeResponse[models.CatalogAgentResponse](t, result)
	if !granted.Trusted || granted.Version != 2 || granted.Package != entry.Package || !granted.CreatedAt.Equal(entry.CreatedAt) ||
		granted.TrustUpdatedBy != "log-user" || granted.TrustUpdatedAt.Before(before) || !granted.TrustUpdatedAt.Equal(granted.UpdatedAt) {
		t.Fatalf("trust grant lost identity or authenticated audit: %+v", granted)
	}

	stale := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":false}`, true)
	result = hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPatch, path, `{"version":2,"trusted":false}`, true)

	assertAPIError(t, stale, http.StatusConflict, "catalog_conflict")
	revoked := decodeResponse[models.CatalogAgentResponse](t, result)
	if revoked.Trusted || revoked.Version != 3 || revoked.Package != granted.Package || !revoked.CreatedAt.Equal(granted.CreatedAt) ||
		revoked.TrustUpdatedBy != "log-user" || revoked.TrustUpdatedAt.Before(granted.TrustUpdatedAt) || !revoked.TrustUpdatedAt.Equal(revoked.UpdatedAt) {
		t.Fatalf("revocation lost identity or audit: %+v", revoked)
	}

	staleGrant := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPatch, path, `{"version":2,"trusted":true}`, true)
	saved := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodGet, path, "", true)

	assertAPIError(t, staleGrant, http.StatusConflict, "catalog_conflict")
	if got := decodeResponse[models.CatalogAgentResponse](t, saved); got != revoked {
		t.Fatalf("stale grant changed the revoked entry: %+v", got)
	}
}

func TestCatalogRejectsMutableTrustGrantsButRecordsRevocations(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	entry := hostfixtures.CatalogEntry(strings.Replace(hostfixtures.CatalogPackage, "@v1.2.3", "@stable", 1))
	if err := config.CatalogStore.Create(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	path := catalogPath + "/" + url.PathEscape(entry.Package)

	grant := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":true}`, true)
	revoke := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":false}`, true)

	assertAPIError(t, grant, http.StatusBadRequest, "invalid_request")
	saved := decodeResponse[models.CatalogAgentResponse](t, revoke)
	if saved.Trusted || saved.Version != 2 || saved.TrustUpdatedBy != "log-user" || saved.TrustUpdatedAt.IsZero() {
		t.Fatalf("mutable selector revocation was not recorded: %+v", saved)
	}
}

func TestCatalogRejectsInvalidInputsBeforeStoreAccess(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	store := fakes.NewCatalogFailures(config.CatalogStore, "*", errors.New("unexpected storage access"))
	config.CatalogStore = store
	handler := hostfixtures.LogHandler(t, config)
	path := catalogPath + "/" + url.PathEscape(hostfixtures.CatalogPackage)
	valid := hostfixtures.CatalogCreateBody(hostfixtures.CatalogPackage)
	for _, test := range []struct {
		name, method, path, body string
	}{
		{"create null", http.MethodPost, catalogPath, "null"},
		{"create empty", http.MethodPost, catalogPath, ""},
		{"create unknown", http.MethodPost, catalogPath, strings.TrimSuffix(valid, "}") + `,"extra":1}`},
		{"create trusted", http.MethodPost, catalogPath, strings.TrimSuffix(valid, "}") + `,"trusted":true}`},
		{"create version", http.MethodPost, catalogPath, strings.TrimSuffix(valid, "}") + `,"version":1}`},
		{"create audit", http.MethodPost, catalogPath, strings.TrimSuffix(valid, "}") + `,"trust_updated_by":"operator"}`},
		{"create visibility", http.MethodPost, catalogPath, strings.Replace(valid, `"private"`, `"trusted"`, 1)},
		{"create name", http.MethodPost, catalogPath, strings.Replace(valid, `"custom-agent"`, `"Custom Agent"`, 1)},
		{"create description", http.MethodPost, catalogPath, strings.Replace(valid, `"A custom worker"`, `" A custom worker"`, 1)},
		{"create omitted release", http.MethodPost, catalogPath, hostfixtures.CatalogCreateBody("github.com/defos-ai/custom-agent")},
		{"create URL", http.MethodPost, catalogPath, hostfixtures.CatalogCreateBody("https://" + hostfixtures.CatalogPackage)},
		{"create query", http.MethodPost, catalogPath + "?trusted=true", valid},
		{"list query", http.MethodGet, catalogPath + "?limit=1", ""},
		{"list malformed query", http.MethodGet, catalogPath + "?%ZZ", ""},
		{"get invalid selector", http.MethodGet, catalogPath + "/" + url.PathEscape("github.com/defos-ai/custom-agent"), ""},
		{"get query", http.MethodGet, path + "?version=1", ""},
		{"trust missing choice", http.MethodPatch, path, `{"version":1}`},
		{"trust null choice", http.MethodPatch, path, `{"version":1,"trusted":null}`},
		{"trust missing version", http.MethodPatch, path, `{"trusted":true}`},
		{"trust zero version", http.MethodPatch, path, `{"version":0,"trusted":true}`},
		{"trust negative version", http.MethodPatch, path, `{"version":-1,"trusted":true}`},
		{"trust fractional version", http.MethodPatch, path, `{"version":1.5,"trusted":true}`},
		{"trust overflowing version", http.MethodPatch, path, `{"version":18446744073709551616,"trusted":true}`},
		{"trust actor", http.MethodPatch, path, `{"version":1,"trusted":true,"trust_updated_by":"someone-else"}`},
		{"trust timestamp", http.MethodPatch, path, `{"version":1,"trusted":true,"trust_updated_at":"2026-10-01T00:00:00Z"}`},
		{"trust metadata", http.MethodPatch, path, `{"version":1,"trusted":true,"name":"renamed-agent"}`},
		{"trust query", http.MethodPatch, path + "?version=1", `{"version":1,"trusted":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {

			result := hostfixtures.CatalogRequest(t.Context(), handler, test.method, test.path, test.body, true)

			assertAPIError(t, result, http.StatusBadRequest, "invalid_request")
		})
	}
	for _, stage := range []string{"create", "get", "list", "replace"} {
		if store.Snapshot(stage).Attempts != 0 {
			t.Fatalf("invalid request reached catalog %s", stage)
		}
	}
}

func TestCatalogPreservesMissingPackagesAndTransportRequirements(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	handler := hostfixtures.LogHandler(t, config)
	path := catalogPath + "/" + url.PathEscape(hostfixtures.CatalogPackage)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, path, ""},
		{http.MethodPatch, path, `{"version":1,"trusted":false}`},
	} {

		result := hostfixtures.CatalogRequest(t.Context(), handler, test.method, test.path, test.body, true)

		assertAPIError(t, result, http.StatusNotFound, "catalog_entry_not_found")
	}
	for _, test := range []struct {
		body, mediaType, code string
		status                int
	}{
		{hostfixtures.CatalogCreateBody(hostfixtures.CatalogPackage), "text/plain", "unsupported_media_type", http.StatusUnsupportedMediaType},
		{strings.Repeat(" ", 1<<20+1), "application/json", "request_too_large", http.StatusRequestEntityTooLarge},
	} {

		result := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPost, catalogPath, test.body, true, test.mediaType)

		assertAPIError(t, result, test.status, test.code)
	}
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, catalogPath, ""}, {http.MethodPost, catalogPath, hostfixtures.CatalogCreateBody(hostfixtures.CatalogPackage)},
		{http.MethodGet, path, ""}, {http.MethodPatch, path, `{"version":1,"trusted":true}`},
	} {

		result := hostfixtures.CatalogRequest(t.Context(), handler, test.method, test.path, test.body, false)

		assertAPIError(t, result, http.StatusUnauthorized, "authentication_required")
	}
	config.CatalogStore = nil
	if _, err := api.NewHandler(config); err == nil {
		t.Fatal("API accepted a missing catalog store")
	}
}

func TestCatalogSanitizesFailuresAndNeverRetriesUncertainWrites(t *testing.T) {
	for _, stage := range []string{"create", "get", "list", "replace"} {
		for _, test := range []struct {
			code   string
			status int
			err    error
		}{
			{"internal_error", http.StatusInternalServerError, errors.New("private backend path and credential")},
			{"request_timeout", http.StatusGatewayTimeout, context.DeadlineExceeded},
			{"request_canceled", http.StatusServiceUnavailable, context.Canceled},
			{"catalog_write_uncertain", http.StatusServiceUnavailable, errors.Join(agents.ErrCatalogWriteUncertain, context.DeadlineExceeded, context.Canceled)},
		} {
			t.Run(stage+"/"+test.code, func(t *testing.T) {
				config := hostfixtures.LogAPIConfig(t)
				if err := config.CatalogStore.Create(t.Context(), hostfixtures.CatalogEntry(hostfixtures.CatalogPackage)); err != nil {
					t.Fatal(err)
				}
				store := fakes.NewCatalogFailures(config.CatalogStore, stage, test.err)
				config.CatalogStore = store
				handler := hostfixtures.LogHandler(t, config)
				method, path, body := http.MethodGet, catalogPath, ""
				switch stage {
				case "create":
					method, body = http.MethodPost, hostfixtures.CatalogCreateBody(hostfixtures.CatalogPackage)
				case "get":
					path += "/" + url.PathEscape(hostfixtures.CatalogPackage)
				case "replace":
					method, path, body = http.MethodPatch, path+"/"+url.PathEscape(hostfixtures.CatalogPackage), `{"version":1,"trusted":false}`
				}

				result := hostfixtures.CatalogRequest(t.Context(), handler, method, path, body, true)

				assertAPIError(t, result, test.status, test.code)
				calls := store.Snapshot(stage)
				if calls.Attempts != 1 || calls.Remaining <= 0 || calls.Remaining > 10*time.Second {
					t.Fatalf("catalog call was retried or lost its deadline: %+v", calls)
				}
				if result.Header.Get("Cache-Control") != "no-store" {
					t.Fatal("catalog failure may be cached")
				}
			})
		}
	}
}

func TestCatalogRemovedRoutesHaveNoAliases(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	entry := hostfixtures.CatalogEntry(hostfixtures.CatalogPackage)
	if err := config.CatalogStore.Create(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	oldPath := "/api/v1/catalog/agents/" + url.PathEscape(entry.Package)
	path := catalogPath + "/" + url.PathEscape(entry.Package)
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/catalog/agents", ""},
		{http.MethodPost, "/api/v1/catalog/agents", hostfixtures.CatalogCreateBody(entry.Package)},
		{http.MethodGet, oldPath, ""},
		{http.MethodPut, oldPath + "/trust", `{"version":1,"trusted":true}`},
		{http.MethodPut, path + "/trust", `{"version":1,"trusted":true}`},
		{http.MethodPatch, path + "/trust", `{"version":1,"trusted":true}`},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {

			result := hostfixtures.CatalogRequest(t.Context(), handler, route.method, route.path, route.body, true)

			if result.Status != http.StatusNotFound {
				t.Fatalf("removed route remains available: status=%d body=%s", result.Status, result.Body)
			}
		})
	}

	put := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodPut, path, `{"version":1,"trusted":true}`, true)
	saved := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodGet, path, "", true)

	if put.Status != http.StatusMethodNotAllowed || !strings.Contains(put.Header.Get("Allow"), http.MethodPatch) || strings.Contains(put.Header.Get("Allow"), http.MethodPut) {
		t.Fatalf("catalog detail must support PATCH without a PUT alias: status=%d allow=%s", put.Status, put.Header.Get("Allow"))
	}
	if current := decodeResponse[models.CatalogAgentResponse](t, saved); current.Version != 1 || current.Trusted || current.TrustUpdatedBy != "" {
		t.Fatalf("removed route changed the saved entry: %+v", current)
	}
}

func TestCatalogOpenAPIMatchesStrictTrustAndMetadataContracts(t *testing.T) {
	handler := hostfixtures.LogHandler(t, hostfixtures.LogAPIConfig(t))
	type schema struct {
		Properties map[string]struct {
			Type     json.RawMessage `json:"type"`
			Maximum  json.RawMessage `json:"maximum"`
			ReadOnly bool            `json:"readOnly"`
		} `json:"properties"`
		Required             []string        `json:"required"`
		AdditionalProperties json.RawMessage `json:"additionalProperties"`
	}
	var document struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
			} `json:"parameters"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}

	result := hostfixtures.CatalogRequest(t.Context(), handler, http.MethodGet, "/openapi.json", "", true)

	if result.Status != http.StatusOK {
		t.Fatalf("catalog OpenAPI document is unavailable: status=%d", result.Status)
	}
	if err := json.Unmarshal([]byte(result.Body), &document); err != nil {
		t.Fatal(err)
	}
	schemas := make(map[string]schema)
	for _, name := range []string{"CreateCatalogAgentRequest", "SetCatalogAgentTrustRequest", "CatalogAgent"} {
		var parsed schema
		if err := json.Unmarshal(document.Components.Schemas[name], &parsed); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
		schemas[name] = parsed
	}
	for _, route := range []struct{ path, method string }{
		{catalogPath, "get"}, {catalogPath, "post"}, {catalogPath + "/{package}", "get"}, {catalogPath + "/{package}", "patch"},
	} {
		operation, ok := document.Paths[route.path][route.method]
		if !ok {
			t.Fatalf("missing documented route %s %s", route.method, route.path)
		}
		for _, parameter := range operation.Parameters {
			if parameter.Name != "package" || parameter.In != "path" || !parameter.Required {
				t.Fatalf("catalog advertises an unsupported parameter: %+v", parameter)
			}
		}
		if operation.Responses["503"] == nil || operation.Responses["504"] == nil || operation.Responses["401"] == nil {
			t.Fatalf("catalog route omitted failure responses: %s", route.path)
		}
	}
	for _, path := range []string{"/api/v1/catalog/agents", "/api/v1/catalog/agents/{package}", "/api/v1/catalog/agents/{package}/trust", catalogPath + "/{package}/trust"} {
		if document.Paths[path] != nil {
			t.Fatalf("removed catalog route remains documented: %s", path)
		}
	}
	if _, exists := document.Paths[catalogPath+"/{package}"]["put"]; exists {
		t.Fatal("catalog detail still documents a PUT alias")
	}
	create := schemas["CreateCatalogAgentRequest"]
	fields := make([]string, 0, len(create.Properties))
	for field := range create.Properties {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	if !slices.Equal(fields, []string{"description", "name", "package", "visibility"}) || string(create.AdditionalProperties) != "false" {
		t.Fatalf("create schema exposes server state or accepts unknown fields: %+v", fields)
	}
	trust := schemas["SetCatalogAgentTrustRequest"]
	if string(trust.Properties["trusted"].Type) != `"boolean"` || string(trust.Properties["version"].Maximum) != "18446744073709551615" ||
		!slices.Contains(trust.Required, "trusted") || !slices.Contains(trust.Required, "version") || string(trust.AdditionalProperties) != "false" {
		t.Fatal("trust schema does not require a nonnull choice and exact uint64 version")
	}
	entry := schemas["CatalogAgent"]
	if len(entry.Properties) != 10 || slices.Contains(entry.Required, "trust_updated_by") || slices.Contains(entry.Required, "trust_updated_at") {
		t.Fatal("catalog metadata lost flat fields or requires fabricated trust audit")
	}
	for name, property := range entry.Properties {
		if !property.ReadOnly {
			t.Errorf("server metadata %s is not read-only", name)
		}
	}
}
