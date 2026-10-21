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
	"github.com/defos-ai/commander/framework/domain/missions"
)

const missionDefinition = `{
  "name": "Research / #1?",
  "objectives": ["Review an action that has not been implemented yet.", "Preserve ordered guidance for later execution."],
  "required_actions": ["inspect-domain", "summarize-domain"],
  "optional_actions": ["publish-review"],
  "starting_actions": ["inspect-domain"],
  "steps": [{
    "id": "inspect",
    "objective": "Inspect the requested domain.",
    "agent_types": ["future-agent"],
    "required_actions": ["inspect-domain"]
  }],
  "rules": {
    "aggregate-results": {"for_actions": ["inspect-domain"]},
    "decide-next-actions": {
      "for_actions": ["summarize-domain"],
      "params": {"decision_by": "model"}
    },
    "same-agent": {"for_actions": ["inspect-domain", "summarize-domain"]}
  },
  "input_schema": {
    "type": "object",
    "properties": {"domain": {"$ref": "#/$defs/domain"}},
    "$defs": {"domain": {"type": "string", "minLength": 1}},
    "required": ["domain"],
    "additionalProperties": false,
    "x-purpose": "Schema extensions belong to the action author."
  },
  "output_schema": {
    "type": "object",
    "properties": {"summary": {"type": "string"}},
    "required": ["summary"]
  }
}`

func TestCreateMissionWithoutInstalledActionsPersistsAcrossRestart(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	created := createMissionRequest(t, server, missionDefinition)
	want := decodeResponse[map[string]any](t, created, http.StatusCreated)
	var definition map[string]any
	if err := json.Unmarshal([]byte(missionDefinition), &definition); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, definition) {
		t.Fatalf("create changed the submitted mission contract: %s", created.body)
	}
	location := "/api/v1/missions/" + url.PathEscape("Research / #1?")
	if created.header.Get("Location") != location {
		t.Fatalf("Location=%q, want %q", created.header.Get("Location"), location)
	}
	assertSecurityHeaders(t, created, false)
	for _, body := range []string{missionDefinition, strings.Replace(missionDefinition, "Review an action that has not been implemented yet.", "Overwrite the saved objective.", 1)} {
		assertAPIError(t, createMissionRequest(t, server, body), http.StatusConflict, "mission_exists")
	}
	get := func() {
		t.Helper()
		actual := decodeResponse[map[string]any](t, server.request(t, http.MethodGet, location, authenticate))
		if !reflect.DeepEqual(actual, want) {
			t.Fatal("mission lookup did not preserve the created definition")
		}
		list := decodeResponse[struct {
			Items []map[string]any `json:"items"`
		}](t, server.request(t, http.MethodGet, "/api/v1/missions", authenticate))
		if len(list.Items) != 1 || !reflect.DeepEqual(list.Items[0], want) {
			t.Fatal("mission list did not return the created definition")
		}
	}
	get()
	server.stop(t)
	server = startAPI(t, fixture.localArgs())
	get()
}

func TestCreateMissionRejectsUnauthorizedAndInvalidDefinitions(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/missions"), http.StatusUnauthorized, "authentication_required")
	assertAPIError(t, createMissionRequest(t, server, missionDefinition, func(request *http.Request) {
		request.Header.Set("Origin", "https://untrusted.example")
	}), http.StatusForbidden, "cross_origin_request")
	for _, tc := range []struct{ name, body string }{
		{"empty body", ""},
		{"null body", "null"},
		{"array body", "[]"},
		{"multiple bodies", missionDefinition + "{}"},
		{"unknown definition field", strings.Replace(missionDefinition, `"objectives":`, `"private-field": "rejected-private-value", "objectives":`, 1)},
		{"unknown step field", strings.Replace(missionDefinition, `"id": "inspect"`, `"id": "inspect", "unexpected": true`, 1)},
		{"unknown rule field", strings.Replace(missionDefinition, `"aggregate-results": {`, `"aggregate-results": {"unexpected": true,`, 1)},
		{"unknown rule parameter", strings.Replace(missionDefinition, `"decision_by": "model"`, `"decision_by": "model", "unexpected": true`, 1)},
		{"missing objectives", strings.Replace(missionDefinition, `"objectives": ["Review an action that has not been implemented yet.", "Preserve ordered guidance for later execution."],`, "", 1)},
		{"legacy objective", strings.Replace(missionDefinition, `"objectives": ["Review an action that has not been implemented yet.", "Preserve ordered guidance for later execution."]`, `"objective": "Legacy single objective"`, 1)},
		{"string objectives", strings.Replace(missionDefinition, `["Review an action that has not been implemented yet.", "Preserve ordered guidance for later execution."]`, `"String objectives"`, 1)},
		{"null objectives", strings.Replace(missionDefinition, `["Review an action that has not been implemented yet.", "Preserve ordered guidance for later execution."]`, `null`, 1)},
		{"empty objectives", strings.Replace(missionDefinition, `["Review an action that has not been implemented yet.", "Preserve ordered guidance for later execution."]`, `[]`, 1)},
		{"blank objective item", strings.Replace(missionDefinition, "Preserve ordered guidance for later execution.", " ", 1)},
		{"dot name", strings.Replace(missionDefinition, `"name": "Research / #1?"`, `"name": "."`, 1)},
		{"parent name", strings.Replace(missionDefinition, `"name": "Research / #1?"`, `"name": ".."`, 1)},
		{"invalid action membership", strings.Replace(missionDefinition, `"starting_actions": ["inspect-domain"]`, `"starting_actions": ["unknown-action"]`, 1)},
		{"invalid rule", strings.Replace(missionDefinition, `"decision_by": "model"`, `"decision_by": "operator"`, 1)},
		{"invalid schema", strings.Replace(missionDefinition, `"type": "string", "minLength": 1`, `"type": "not-a-schema-type"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertAPIError(t, createMissionRequest(t, server, tc.body), http.StatusBadRequest, "invalid_request")
		})
	}
	assertAPIError(t, createMissionRequest(t, server, missionDefinition, func(request *http.Request) {
		request.URL.RawQuery = "overwrite=true"
	}), http.StatusBadRequest, "invalid_request")
	for _, kind := range []string{"", "text/plain"} {
		result := createMissionRequest(t, server, missionDefinition, func(request *http.Request) {
			request.Header.Del("Content-Type")
			if kind != "" {
				request.Header.Set("Content-Type", kind)
			}
		})
		assertAPIError(t, result, http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	large := strings.Replace(missionDefinition, "Review an action that has not been implemented yet.", strings.Repeat("x", 1<<20), 1)
	assertAPIError(t, createMissionRequest(t, server, large), http.StatusRequestEntityTooLarge, "request_too_large")
	list := decodeResponse[struct {
		Items []json.RawMessage `json:"items"`
	}](t, server.request(t, http.MethodGet, "/api/v1/missions", authenticate))
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
			store := &failedMissionCreate{err: tc.err}
			mux := http.NewServeMux()
			endpoints.RegisterMissions(mux, store)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/missions", strings.NewReader(missionDefinition))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			assertAPIError(t, response{status: recorder.Code, header: recorder.Header(), body: recorder.Body.String()}, tc.status, tc.code)
			if store.attempts != 1 || store.remaining <= 0 || store.remaining > 10*time.Second {
				t.Fatal("mission creation was retried or lost its bounded request context")
			}
			if recorder.Header().Get("Location") != "" {
				t.Fatal("failed mission creation returned a success location")
			}
		})
	}
}

type failedMissionCreate struct {
	missions.MissionStore
	err       error
	attempts  int
	remaining time.Duration
}

func (store *failedMissionCreate) Create(ctx context.Context, _ missions.Mission) error {
	store.attempts++
	deadline, _ := ctx.Deadline()
	store.remaining = time.Until(deadline)
	return store.err
}

func createMissionRequest(t *testing.T, server *runningAPI, body string, configure ...func(*http.Request)) response {
	t.Helper()
	changes := []func(*http.Request){authenticate, func(request *http.Request) {
		request.Body = io.NopCloser(strings.NewReader(body))
		request.ContentLength = int64(len(body))
		request.Header.Set("Content-Type", "application/json")
	}}
	return server.request(t, http.MethodPost, "/api/v1/missions", append(changes, configure...)...)
}
