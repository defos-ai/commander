package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestCreateMissionWithoutInstalledActionsPersistsAcrossRestart(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	created := createMissionRequest(t, server, hostfixtures.MissionDefinition)
	want := decodeResponse[map[string]any](t, created, http.StatusCreated)
	var definition map[string]any
	if err := json.Unmarshal([]byte(hostfixtures.MissionDefinition), &definition); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, definition) {
		t.Fatalf("create changed the submitted mission contract: %s", created.Body)
	}
	location := "/api/v1/missions/" + url.PathEscape("Research / #1?")
	if created.Header.Get("Location") != location {
		t.Fatalf("Location=%q, want %q", created.Header.Get("Location"), location)
	}
	assertSecurityHeaders(t, created, false)
	for _, lexical := range []string{"9007199254740993", "1.2300e-6", `\u0061`} {
		if !strings.Contains(created.Body, lexical) {
			t.Fatalf("action definition schema changed lexical JSON value %q: %s", lexical, created.Body)
		}
	}
	for _, body := range []string{hostfixtures.MissionDefinition, strings.Replace(hostfixtures.MissionDefinition, "Review an action that has not been implemented yet.", "Overwrite the saved objective.", 1)} {
		assertAPIError(t, createMissionRequest(t, server, body), http.StatusConflict, "mission_exists")
	}
	get := func() {
		t.Helper()
		actual := decodeResponse[map[string]any](t, server.Request(t, http.MethodGet, location, hostfixtures.Authenticate))
		if !reflect.DeepEqual(actual, want) {
			t.Fatal("mission lookup did not preserve the created definition")
		}
		list := decodeResponse[struct {
			Items []map[string]any `json:"items"`
		}](t, server.Request(t, http.MethodGet, "/api/v1/missions", hostfixtures.Authenticate))
		if len(list.Items) != 1 || !reflect.DeepEqual(list.Items[0], want) {
			t.Fatal("mission list did not return the created definition")
		}
		for _, resource := range []string{"actions", "agents", "operations"} {
			catalog := decodeResponse[struct {
				Items []json.RawMessage `json:"items"`
			}](t, server.Request(t, http.MethodGet, "/api/v1/"+resource, hostfixtures.Authenticate))
			if len(catalog.Items) != 0 {
				t.Fatalf("defining mission actions unexpectedly created %s", resource)
			}
		}
	}
	get()
	server.Stop(t)
	server = hostfixtures.Start(t, fixture.LocalArgs())
	get()
}

func TestCreateMissionRejectsUnauthorizedAndInvalidDefinitions(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	assertAPIError(t, server.Request(t, http.MethodPost, "/api/v1/missions"), http.StatusUnauthorized, "authentication_required")
	assertAPIError(t, createMissionRequest(t, server, hostfixtures.MissionDefinition, func(request *http.Request) {
		request.Header.Set("Origin", "https://untrusted.example")
	}), http.StatusForbidden, "cross_origin_request")
	for _, tc := range []struct{ name, body string }{
		{"empty body", ""},
		{"null body", "null"},
		{"array body", "[]"},
		{"multiple bodies", hostfixtures.MissionDefinition + "{}"},
		{"unknown definition field", strings.Replace(hostfixtures.MissionDefinition, `"objectives":`, `"private-field": "rejected-private-value", "objectives":`, 1)},
		{"action readiness field", strings.Replace(hostfixtures.MissionDefinition, `"name": "inspect-domain"`, `"name": "inspect-domain", "status": "ready"`, 1)},
		{"unreferenced action definition", strings.Replace(hostfixtures.MissionDefinition, `"name": "publish-review"`, `"name": "unreferenced-action"`, 1)},
		{"remote action definition reference", strings.Replace(hostfixtures.MissionDefinition, `"output_schema": {"type":"object","x-note":"\u0061"}`, `"output_schema": {"$ref":"https://private.example/schema"}`, 1)},
		{"unknown step field", strings.Replace(hostfixtures.MissionDefinition, `"id": "inspect"`, `"id": "inspect", "unexpected": true`, 1)},
		{"unknown rule field", strings.Replace(hostfixtures.MissionDefinition, `"aggregate-results": {`, `"aggregate-results": {"unexpected": true,`, 1)},
		{"unknown rule parameter", strings.Replace(hostfixtures.MissionDefinition, `"decision_by": "model"`, `"decision_by": "model", "unexpected": true`, 1)},
		{"missing objectives", strings.Replace(hostfixtures.MissionDefinition, `"objectives": ["Review an action that has not been implemented yet.", "Preserve ordered guidance for later execution."],`, "", 1)},
		{"string objectives", strings.Replace(hostfixtures.MissionDefinition, `["Review an action that has not been implemented yet.", "Preserve ordered guidance for later execution."]`, `"String objectives"`, 1)},
		{"invalid action membership", strings.Replace(hostfixtures.MissionDefinition, `"starting_actions": ["inspect-domain"]`, `"starting_actions": ["unknown-action"]`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertAPIError(t, createMissionRequest(t, server, tc.body), http.StatusBadRequest, "invalid_request")
		})
	}
	assertAPIError(t, createMissionRequest(t, server, hostfixtures.MissionDefinition, func(request *http.Request) {
		request.URL.RawQuery = "overwrite=true"
	}), http.StatusBadRequest, "invalid_request")
	for _, kind := range []string{"", "text/plain"} {
		result := createMissionRequest(t, server, hostfixtures.MissionDefinition, func(request *http.Request) {
			request.Header.Del("Content-Type")
			if kind != "" {
				request.Header.Set("Content-Type", kind)
			}
		})
		assertAPIError(t, result, http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	large := strings.Replace(hostfixtures.MissionDefinition, "Review an action that has not been implemented yet.", strings.Repeat("x", 1<<20), 1)
	assertAPIError(t, createMissionRequest(t, server, large), http.StatusRequestEntityTooLarge, "request_too_large")
	list := decodeResponse[struct {
		Items []json.RawMessage `json:"items"`
	}](t, server.Request(t, http.MethodGet, "/api/v1/missions", hostfixtures.Authenticate))
	if len(list.Items) != 0 {
		t.Fatal("rejected mission creation changed the saved catalog")
	}
}

func TestCreateMissionStoreFailuresAreSanitizedAndNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		code   string
		err    error
		status int
	}{
		{"request_canceled", context.Canceled, http.StatusServiceUnavailable},
		{"request_timeout", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"internal_error", errors.New("private database credentials and path"), http.StatusInternalServerError},
	} {
		t.Run(tc.code, func(t *testing.T) {
			store := fakes.NewFailedMissionCreate(tc.err)
			mux := http.NewServeMux()
			endpoints.RegisterMissions(mux, store)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/missions", strings.NewReader(hostfixtures.MissionDefinition))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			mux.ServeHTTP(recorder, request)

			assertAPIError(t, hostfixtures.Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}, tc.status, tc.code)
			calls := store.Snapshot()
			if calls.Attempts != 1 || calls.Remaining <= 0 || calls.Remaining > 10*time.Second {
				t.Fatal("mission creation was retried or lost its bounded request context")
			}
			if recorder.Header().Get("Location") != "" {
				t.Fatal("failed mission creation returned a success location")
			}
		})
	}
}

func createMissionRequest(t *testing.T, server *hostfixtures.API, body string, configure ...func(*http.Request)) hostfixtures.Response {
	t.Helper()
	changes := []func(*http.Request){hostfixtures.Authenticate, func(request *http.Request) {
		request.Body = io.NopCloser(strings.NewReader(body))
		request.ContentLength = int64(len(body))
		request.Header.Set("Content-Type", "application/json")
	}}
	return server.Request(t, http.MethodPost, "/api/v1/missions", append(changes, configure...)...)
}
