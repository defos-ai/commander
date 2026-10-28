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

const agentSourcesPath = "/api/v1/agent-sources"

func TestAgentSourcesCreatesUntrustedSourcesAndReadsSavedEntries(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	handler := hostfixtures.LogHandler(t, config)
	before := time.Now()

	empty := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodGet, agentSourcesPath, "", true)

	if items := decodeResponse[models.ListResponse[models.AgentSourceResponse]](t, empty).Items; items == nil || len(items) != 0 {
		t.Fatalf("empty source list must return an empty array: %s", empty.Body)
	}
	var created models.AgentSourceResponse
	for _, value := range []string{hostfixtures.AgentSourceSelector, strings.Replace(hostfixtures.AgentSourceSelector, "@v1.2.3", "@stable", 1)} {
		body := hostfixtures.AgentSourceCreateBody(value)

		result := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPost, agentSourcesPath, body, true, "application/json; charset=utf-8")

		entry := decodeResponse[models.AgentSourceResponse](t, result, http.StatusCreated)
		if entry.Source != value || entry.Name != "custom-agent" || entry.Description != "A custom worker" || entry.Visibility != "private" ||
			entry.Trusted || entry.Version != 1 || !entry.CreatedAt.Equal(entry.UpdatedAt) || entry.CreatedAt.Before(before) || entry.CreatedAt.After(time.Now()) ||
			entry.TrustUpdatedBy != "" || !entry.TrustUpdatedAt.IsZero() {
			t.Fatalf("creation did not assign safe defaults: %+v", entry)
		}
		if result.Header.Get("Location") != agentSourcesPath+"/"+url.PathEscape(value) || result.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("creation lost its escaped location or cache policy: %v", result.Header)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(result.Body), &fields) != nil || fields["trust_updated_by"] != nil || fields["trust_updated_at"] != nil {
			t.Fatal("creation fabricated a trust decision")
		}

		duplicate := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPost, agentSourcesPath, body, true)

		assertAPIError(t, duplicate, http.StatusConflict, "agent_source_exists")
		if value == hostfixtures.AgentSourceSelector {
			created = entry
		}
	}
	reopened := hostfixtures.LogHandler(t, config)

	get := hostfixtures.AgentSourceRequest(t.Context(), reopened, http.MethodGet, agentSourcesPath+"/"+url.PathEscape(created.Source), "", true)
	list := hostfixtures.AgentSourceRequest(t.Context(), reopened, http.MethodGet, agentSourcesPath, "", true)

	if saved := decodeResponse[models.AgentSourceResponse](t, get); saved != created {
		t.Fatalf("saved package changed across API hosts: got %+v, want %+v", saved, created)
	}
	items := decodeResponse[models.ListResponse[models.AgentSourceResponse]](t, list).Items
	if len(items) != 2 || items[0].Source >= items[1].Source {
		t.Fatalf("sources must distinguish and sort full selectors: %+v", items)
	}
	if config.Operations.(*fakes.FailedStart).Snapshot().Attempts != 0 {
		t.Fatal("source access contacted the controller")
	}
}

func TestAgentSourcesTrustDecisionsPreserveIdentityAndRejectStaleRevocations(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	entry := hostfixtures.AgentSource(hostfixtures.AgentSourceSelector)
	if err := config.AgentSourceStore.Create(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	path := agentSourcesPath + "/" + url.PathEscape(entry.Source)
	before := time.Now()

	result := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":true}`, true)

	granted := decodeResponse[models.AgentSourceResponse](t, result)
	if !granted.Trusted || granted.Version != 2 || granted.Source != entry.Source || !granted.CreatedAt.Equal(entry.CreatedAt) ||
		granted.TrustUpdatedBy != "log-user" || granted.TrustUpdatedAt.Before(before) || !granted.TrustUpdatedAt.Equal(granted.UpdatedAt) {
		t.Fatalf("trust grant lost identity or authenticated audit: %+v", granted)
	}

	stale := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":false}`, true)
	result = hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPatch, path, `{"version":2,"trusted":false}`, true)

	assertAPIError(t, stale, http.StatusConflict, "agent_source_conflict")
	revoked := decodeResponse[models.AgentSourceResponse](t, result)
	if revoked.Trusted || revoked.Version != 3 || revoked.Source != granted.Source || !revoked.CreatedAt.Equal(granted.CreatedAt) ||
		revoked.TrustUpdatedBy != "log-user" || revoked.TrustUpdatedAt.Before(granted.TrustUpdatedAt) || !revoked.TrustUpdatedAt.Equal(revoked.UpdatedAt) {
		t.Fatalf("revocation lost identity or audit: %+v", revoked)
	}

	staleGrant := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPatch, path, `{"version":2,"trusted":true}`, true)
	saved := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodGet, path, "", true)

	assertAPIError(t, staleGrant, http.StatusConflict, "agent_source_conflict")
	if got := decodeResponse[models.AgentSourceResponse](t, saved); got != revoked {
		t.Fatalf("stale grant changed the revoked entry: %+v", got)
	}
}

func TestAgentSourcesRejectsMutableTrustGrantsButRecordsRevocations(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	entry := hostfixtures.AgentSource(strings.Replace(hostfixtures.AgentSourceSelector, "@v1.2.3", "@stable", 1))
	if err := config.AgentSourceStore.Create(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	path := agentSourcesPath + "/" + url.PathEscape(entry.Source)

	grant := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":true}`, true)
	revoke := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPatch, path, `{"version":1,"trusted":false}`, true)

	assertAPIError(t, grant, http.StatusBadRequest, "invalid_request")
	saved := decodeResponse[models.AgentSourceResponse](t, revoke)
	if saved.Trusted || saved.Version != 2 || saved.TrustUpdatedBy != "log-user" || saved.TrustUpdatedAt.IsZero() {
		t.Fatalf("mutable selector revocation was not recorded: %+v", saved)
	}
}

func TestAgentSourcesRejectsInvalidInputsBeforeStoreAccess(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	store := fakes.NewAgentSourceFailures(config.AgentSourceStore, "*", errors.New("unexpected storage access"))
	config.AgentSourceStore = store
	handler := hostfixtures.LogHandler(t, config)
	path := agentSourcesPath + "/" + url.PathEscape(hostfixtures.AgentSourceSelector)
	valid := hostfixtures.AgentSourceCreateBody(hostfixtures.AgentSourceSelector)
	for _, test := range []struct {
		name, method, path, body string
	}{
		{"create null", http.MethodPost, agentSourcesPath, "null"},
		{"create empty", http.MethodPost, agentSourcesPath, ""},
		{"create unknown", http.MethodPost, agentSourcesPath, strings.TrimSuffix(valid, "}") + `,"extra":1}`},
		{"create trusted", http.MethodPost, agentSourcesPath, strings.TrimSuffix(valid, "}") + `,"trusted":true}`},
		{"create version", http.MethodPost, agentSourcesPath, strings.TrimSuffix(valid, "}") + `,"version":1}`},
		{"create audit", http.MethodPost, agentSourcesPath, strings.TrimSuffix(valid, "}") + `,"trust_updated_by":"operator"}`},
		{"create visibility", http.MethodPost, agentSourcesPath, strings.Replace(valid, `"private"`, `"trusted"`, 1)},
		{"create name", http.MethodPost, agentSourcesPath, strings.Replace(valid, `"custom-agent"`, `"Custom Agent"`, 1)},
		{"create description", http.MethodPost, agentSourcesPath, strings.Replace(valid, `"A custom worker"`, `" A custom worker"`, 1)},
		{"create omitted release", http.MethodPost, agentSourcesPath, hostfixtures.AgentSourceCreateBody("github.com/defos-ai/custom-agent")},
		{"create URL", http.MethodPost, agentSourcesPath, hostfixtures.AgentSourceCreateBody("https://" + hostfixtures.AgentSourceSelector)},
		{"create legacy package field", http.MethodPost, agentSourcesPath, strings.Replace(valid, `"source"`, `"package"`, 1)},
		{"create query", http.MethodPost, agentSourcesPath + "?trusted=true", valid},
		{"list query", http.MethodGet, agentSourcesPath + "?limit=1", ""},
		{"list malformed query", http.MethodGet, agentSourcesPath + "?%ZZ", ""},
		{"get invalid selector", http.MethodGet, agentSourcesPath + "/" + url.PathEscape("github.com/defos-ai/custom-agent"), ""},
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

			result := hostfixtures.AgentSourceRequest(t.Context(), handler, test.method, test.path, test.body, true)

			assertAPIError(t, result, http.StatusBadRequest, "invalid_request")
		})
	}
	for _, stage := range []string{"create", "get", "list", "replace"} {
		if store.Snapshot(stage).Attempts != 0 {
			t.Fatalf("invalid request reached source %s", stage)
		}
	}
}

func TestAgentSourcesPreservesMissingSourcesAndTransportRequirements(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	handler := hostfixtures.LogHandler(t, config)
	path := agentSourcesPath + "/" + url.PathEscape(hostfixtures.AgentSourceSelector)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, path, ""},
		{http.MethodPatch, path, `{"version":1,"trusted":false}`},
	} {

		result := hostfixtures.AgentSourceRequest(t.Context(), handler, test.method, test.path, test.body, true)

		assertAPIError(t, result, http.StatusNotFound, "agent_source_not_found")
	}
	for _, test := range []struct {
		body, mediaType, code string
		status                int
	}{
		{hostfixtures.AgentSourceCreateBody(hostfixtures.AgentSourceSelector), "text/plain", "unsupported_media_type", http.StatusUnsupportedMediaType},
		{strings.Repeat(" ", 1<<20+1), "application/json", "request_too_large", http.StatusRequestEntityTooLarge},
	} {

		result := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPost, agentSourcesPath, test.body, true, test.mediaType)

		assertAPIError(t, result, test.status, test.code)
	}
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, agentSourcesPath, ""}, {http.MethodPost, agentSourcesPath, hostfixtures.AgentSourceCreateBody(hostfixtures.AgentSourceSelector)},
		{http.MethodGet, path, ""}, {http.MethodPatch, path, `{"version":1,"trusted":true}`},
	} {

		result := hostfixtures.AgentSourceRequest(t.Context(), handler, test.method, test.path, test.body, false)

		assertAPIError(t, result, http.StatusUnauthorized, "authentication_required")
	}
	config.AgentSourceStore = nil
	if _, err := api.NewHandler(config); err == nil {
		t.Fatal("API accepted a missing agent source store")
	}
}

func TestAgentSourcesSanitizesFailuresAndNeverRetriesUncertainWrites(t *testing.T) {
	for _, stage := range []string{"create", "get", "list", "replace"} {
		for _, test := range []struct {
			code   string
			status int
			err    error
		}{
			{"internal_error", http.StatusInternalServerError, errors.New("private backend path and credential")},
			{"request_timeout", http.StatusGatewayTimeout, context.DeadlineExceeded},
			{"request_canceled", http.StatusServiceUnavailable, context.Canceled},
			{"agent_source_write_uncertain", http.StatusServiceUnavailable, errors.Join(agents.ErrAgentSourceWriteUncertain, context.DeadlineExceeded, context.Canceled)},
		} {
			t.Run(stage+"/"+test.code, func(t *testing.T) {
				config := hostfixtures.LogAPIConfig(t)
				if err := config.AgentSourceStore.Create(t.Context(), hostfixtures.AgentSource(hostfixtures.AgentSourceSelector)); err != nil {
					t.Fatal(err)
				}
				store := fakes.NewAgentSourceFailures(config.AgentSourceStore, stage, test.err)
				config.AgentSourceStore = store
				handler := hostfixtures.LogHandler(t, config)
				method, path, body := http.MethodGet, agentSourcesPath, ""
				switch stage {
				case "create":
					method, body = http.MethodPost, hostfixtures.AgentSourceCreateBody(hostfixtures.AgentSourceSelector)
				case "get":
					path += "/" + url.PathEscape(hostfixtures.AgentSourceSelector)
				case "replace":
					method, path, body = http.MethodPatch, path+"/"+url.PathEscape(hostfixtures.AgentSourceSelector), `{"version":1,"trusted":false}`
				}

				result := hostfixtures.AgentSourceRequest(t.Context(), handler, method, path, body, true)

				assertAPIError(t, result, test.status, test.code)
				calls := store.Snapshot(stage)
				if calls.Attempts != 1 || calls.Remaining <= 0 || calls.Remaining > 10*time.Second {
					t.Fatalf("source call was retried or lost its deadline: %+v", calls)
				}
				if result.Header.Get("Cache-Control") != "no-store" {
					t.Fatal("source failure may be cached")
				}
			})
		}
	}
}

func TestAgentSourcesRemovedRoutesHaveNoAliases(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	entry := hostfixtures.AgentSource(hostfixtures.AgentSourceSelector)
	if err := config.AgentSourceStore.Create(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	oldPath := "/api/v1/catalog/agents/" + url.PathEscape(entry.Source)
	path := agentSourcesPath + "/" + url.PathEscape(entry.Source)
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/catalog-agents", ""},
		{http.MethodPost, "/api/v1/catalog-agents", hostfixtures.AgentSourceCreateBody(entry.Source)},
		{http.MethodGet, "/api/v1/catalog-agents/" + url.PathEscape(entry.Source), ""},
		{http.MethodPatch, "/api/v1/catalog-agents/" + url.PathEscape(entry.Source), `{"version":1,"trusted":true}`},
		{http.MethodGet, "/api/v1/catalog/agents", ""},
		{http.MethodPost, "/api/v1/catalog/agents", hostfixtures.AgentSourceCreateBody(entry.Source)},
		{http.MethodGet, oldPath, ""},
		{http.MethodPut, oldPath + "/trust", `{"version":1,"trusted":true}`},
		{http.MethodPut, path + "/trust", `{"version":1,"trusted":true}`},
		{http.MethodPatch, path + "/trust", `{"version":1,"trusted":true}`},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {

			result := hostfixtures.AgentSourceRequest(t.Context(), handler, route.method, route.path, route.body, true)

			if result.Status != http.StatusNotFound {
				t.Fatalf("removed route remains available: status=%d body=%s", result.Status, result.Body)
			}
		})
	}

	put := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodPut, path, `{"version":1,"trusted":true}`, true)
	saved := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodGet, path, "", true)

	if put.Status != http.StatusMethodNotAllowed || !strings.Contains(put.Header.Get("Allow"), http.MethodPatch) || strings.Contains(put.Header.Get("Allow"), http.MethodPut) {
		t.Fatalf("source detail must support PATCH without a PUT alias: status=%d allow=%s", put.Status, put.Header.Get("Allow"))
	}
	if current := decodeResponse[models.AgentSourceResponse](t, saved); current.Version != 1 || current.Trusted || current.TrustUpdatedBy != "" {
		t.Fatalf("removed route changed the saved entry: %+v", current)
	}
}

func TestAgentSourcesOpenAPIMatchesStrictTrustAndMetadataContracts(t *testing.T) {
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
			OperationID string `json:"operationId"`
			Parameters  []struct {
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

	result := hostfixtures.AgentSourceRequest(t.Context(), handler, http.MethodGet, "/openapi.json", "", true)

	if result.Status != http.StatusOK {
		t.Fatalf("source OpenAPI document is unavailable: status=%d", result.Status)
	}
	if err := json.Unmarshal([]byte(result.Body), &document); err != nil {
		t.Fatal(err)
	}
	schemas := make(map[string]schema)
	for _, name := range []string{"CreateAgentSourceRequest", "SetAgentSourceTrustRequest", "AgentSource"} {
		var parsed schema
		if err := json.Unmarshal(document.Components.Schemas[name], &parsed); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
		schemas[name] = parsed
	}
	for _, route := range []struct{ path, method, operationID string }{
		{agentSourcesPath, "get", "listAgentSources"}, {agentSourcesPath, "post", "createAgentSource"},
		{agentSourcesPath + "/{source}", "get", "getAgentSource"}, {agentSourcesPath + "/{source}", "patch", "setAgentSourceTrust"},
	} {
		operation, ok := document.Paths[route.path][route.method]
		if !ok || operation.OperationID != route.operationID {
			t.Fatalf("missing documented route %s %s", route.method, route.path)
		}
		for _, parameter := range operation.Parameters {
			if parameter.Name != "source" || parameter.In != "path" || !parameter.Required {
				t.Fatalf("source endpoint advertises an unsupported parameter: %+v", parameter)
			}
		}
		if operation.Responses["503"] == nil || operation.Responses["504"] == nil || operation.Responses["401"] == nil {
			t.Fatalf("source route omitted failure responses: %s", route.path)
		}
	}
	for _, path := range []string{"/api/v1/catalog-agents", "/api/v1/catalog-agents/{package}", "/api/v1/catalog/agents", "/api/v1/catalog/agents/{package}", "/api/v1/catalog/agents/{package}/trust", agentSourcesPath + "/{source}/trust"} {
		if document.Paths[path] != nil {
			t.Fatalf("removed catalog route remains documented: %s", path)
		}
	}
	if _, exists := document.Paths[agentSourcesPath+"/{source}"]["put"]; exists {
		t.Fatal("source detail still documents a PUT alias")
	}
	for _, name := range []string{"CatalogAgent", "CreateCatalogAgentRequest", "SetCatalogAgentTrustRequest"} {
		if document.Components.Schemas[name] != nil {
			t.Fatalf("removed catalog schema remains documented: %s", name)
		}
	}
	create := schemas["CreateAgentSourceRequest"]
	fields := make([]string, 0, len(create.Properties))
	for field := range create.Properties {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	if !slices.Equal(fields, []string{"description", "name", "source", "visibility"}) || string(create.AdditionalProperties) != "false" {
		t.Fatalf("create schema exposes server state or accepts unknown fields: %+v", fields)
	}
	trust := schemas["SetAgentSourceTrustRequest"]
	if string(trust.Properties["trusted"].Type) != `"boolean"` || string(trust.Properties["version"].Maximum) != "18446744073709551615" ||
		!slices.Contains(trust.Required, "trusted") || !slices.Contains(trust.Required, "version") || string(trust.AdditionalProperties) != "false" {
		t.Fatal("trust schema does not require a nonnull choice and exact uint64 version")
	}
	entry := schemas["AgentSource"]
	if len(entry.Properties) != 10 || slices.Contains(entry.Required, "trust_updated_by") || slices.Contains(entry.Required, "trust_updated_at") {
		t.Fatal("source metadata lost flat fields or requires fabricated trust audit")
	}
	for name, property := range entry.Properties {
		if !property.ReadOnly {
			t.Errorf("server metadata %s is not read-only", name)
		}
	}
}
