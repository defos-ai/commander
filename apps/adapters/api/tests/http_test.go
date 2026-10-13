package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestReadOnlyQueriesValidateIDsAndEmptyCollections(t *testing.T) {
	a := connect(t)
	op := a.start(website(t))
	base := operationPath(op.ID)
	before := a.request(http.MethodGet, base, nil)
	lastEvent := fmt.Sprint(op.Events[len(op.Events)-1].Sequence)
	for _, tc := range []struct{ path, cursor string }{{base + "/tasks", ""}, {base + "/events?cursor=" + lastEvent, lastEvent}} {
		page := read[map[string]json.RawMessage](a, tc.path)
		if string(page["items"]) != "[]" || string(page["next_cursor"]) != fmt.Sprintf("%q", tc.cursor) {
			t.Fatalf("empty page shape: %+v", page)
		}
	}
	questions := read[models.QuestionListResponse](a, base+"/questions")
	if questions.Items == nil || questions.LastQuestion != nil || questions.RemainingQuestions != 0 {
		t.Fatalf("empty question shape: %+v", questions)
	}
	for _, suffix := range []string{"", "/tasks", "/tasks/" + absentID, "/events", "/questions", "/questions/" + absentID, "/graph", "/report"} {
		assertError(t, a.request(http.MethodGet, operationPath(absentID)+suffix, nil), http.StatusNotFound, "operation_not_found")
		for _, invalid := range []string{"not-a-uuid", "00000000-0000-4000-8000-000000ABCDEF", strings.ReplaceAll(op.ID, "-", "")} {
			assertError(t, a.request(http.MethodGet, operationPath(invalid)+suffix, nil), http.StatusBadRequest, "invalid_operation_id")
		}
	}
	for _, tc := range []struct{ path, code string }{
		{base + "/tasks/" + absentID, "task_not_found"}, {base + "/questions/" + absentID, "question_not_found"}, {base + "/report", "report_not_found"},
		{"/api/v1/reports/" + absentID, "report_not_found"}, {"/api/v1/artifacts/" + absentID, "artifact_not_found"},
	} {
		assertError(t, a.request(http.MethodGet, tc.path, nil), http.StatusNotFound, tc.code)
	}
	for _, suffix := range []string{"/tasks", "/events"} {
		for _, query := range []string{"limit=0", "limit=1001", "limit=bad"} {
			assertError(t, a.request(http.MethodGet, base+suffix+"?"+query, nil), http.StatusBadRequest, "invalid_limit")
		}
		for _, query := range []string{"unknown=1", "cursor=&cursor=", "limit=1&limit=2"} {
			assertError(t, a.request(http.MethodGet, base+suffix+"?"+query, nil), http.StatusBadRequest, "invalid_query")
		}
	}
	assertError(t, a.request(http.MethodGet, base+"/tasks?cursor=invalid", nil), http.StatusBadRequest, "invalid_cursor")
	assertError(t, a.request(http.MethodGet, base+"/events?cursor=-1", nil), http.StatusBadRequest, "invalid_cursor")
	assertError(t, a.request(http.MethodGet, base+"/tasks?cursor="+absentID, nil), http.StatusNotFound, "task_not_found")
	if after := a.request(http.MethodGet, base, nil); after.body != before.body {
		t.Fatal("read-only/error queries mutated saved operation")
	}
}

func TestHTTPAuthenticationOriginMethodsAndPublicProbes(t *testing.T) {
	a := connect(t)
	op := a.start(website(t))
	paths := []string{"/api/v1/missions", "/api/v1/actions", "/api/v1/agents", "/api/v1/controllers", "/api/v1/controllers/unknown-controller", operationPath(op.ID), operationPath(op.ID) + "/graph", "/api/v1/reports/" + absentID, "/api/v1/artifacts/" + absentID, "/openapi.json", "/api/v1/unknown"}
	for _, path := range paths {
		unauthorized := a.request(http.MethodGet, path, nil, func(r *http.Request) { r.Header.Del("Authorization") })
		assertError(t, unauthorized, http.StatusUnauthorized, "authentication_required")
		assertHeaders(t, unauthorized)
		if unauthorized.header.Get("WWW-Authenticate") != `Basic realm="Commander", charset="UTF-8"` {
			t.Fatal("authentication challenge changed")
		}
		assertError(t, a.request(http.MethodGet, path, nil, func(r *http.Request) { r.SetBasicAuth(a.username, "wrong-password") }), http.StatusUnauthorized, "authentication_required")
		assertError(t, a.request(http.MethodGet, path, nil, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }), http.StatusForbidden, "cross_origin_request")
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if result := a.request(method, operationPath(op.ID)+"/graph", nil); result.status != http.StatusMethodNotAllowed {
			t.Fatalf("unsupported method %s: %+v", method, result)
		}
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		result := a.request(http.MethodGet, path, nil, func(r *http.Request) { r.Header.Del("Authorization") })
		if body := decode[map[string]string](t, result, http.StatusOK); body["status"] != "ok" {
			t.Fatalf("unhealthy probe: %+v", body)
		}
		head := a.request(http.MethodHead, path, nil, func(r *http.Request) { r.Header.Del("Authorization") })
		if head.status != http.StatusOK || head.body != "" {
			t.Fatalf("HEAD probe: %+v", head)
		}
	}
	about := a.request(http.MethodGet, "/aboutz", nil, func(r *http.Request) { r.Header.Del("Authorization") })
	deployment := decode[map[string]string](t, about, http.StatusOK)
	if len(deployment) != 2 || deployment["runtime"] != "docker-compose" || deployment["application"] != "commander-api" {
		t.Fatalf("unexpected public deployment metadata: %+v", deployment)
	}
	assertHeaders(t, about)
	assertSchema(t, a, "About", about)
	assertError(t, a.request(http.MethodPost, "/api/v1/testing/cleanup", nil, func(r *http.Request) { r.Header.Del("Authorization") }), http.StatusUnauthorized, "authentication_required")
	assertError(t, a.request(http.MethodPost, "/api/v1/testing/cleanup", nil, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }), http.StatusForbidden, "cross_origin_request")
	if result := a.request(http.MethodGet, "/api/v1/testing/cleanup", nil); result.status != http.StatusMethodNotAllowed {
		t.Fatalf("cleanup must require POST: %+v", result)
	}
}

func TestSwaggerServesProtectedLocalAssetsAndOperationContracts(t *testing.T) {
	a := connect(t)
	for _, path := range []string{"/docs/", "/docs/swagger-ui.css", "/docs/swagger-ui-bundle.js", "/docs/initializer.js", "/openapi.json"} {
		result := a.request(http.MethodGet, path, nil)
		if result.status != http.StatusOK || result.body == "" {
			t.Fatalf("Swagger resource %s: %+v", path, result)
		}
		assertError(t, a.request(http.MethodGet, path, nil, func(r *http.Request) { r.Header.Del("Authorization") }), http.StatusUnauthorized, "authentication_required")
	}
	document := read[struct {
		OpenAPI  string                `json:"openapi"`
		Security []map[string][]string `json:"security"`
		Paths    map[string]struct {
			Get struct {
				Responses map[string]json.RawMessage `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]jsonschema.Schema `json:"schemas"`
		} `json:"components"`
	}](a, "/openapi.json")
	if !strings.HasPrefix(document.OpenAPI, "3.") || len(document.Security) != 1 {
		t.Fatalf("OpenAPI version/security: %+v", document)
	}
	if _, ok := document.Security[0]["basicAuth"]; !ok {
		t.Fatal("OpenAPI does not declare Basic authentication")
	}
	mission := document.Components.Schemas["Mission"]
	if mission.Properties["rules"] == nil || mission.Properties["automations"] != nil {
		t.Fatal("OpenAPI must publish mission rules without the old automation format")
	}
	params := document.Components.Schemas["MissionRuleParams"]
	if decision := params.Properties["decision_by"]; decision == nil || len(decision.Enum) != 1 || decision.Enum[0] != "model" {
		t.Fatal("OpenAPI must document model as the supported rule decision maker")
	}
	for _, path := range []string{"/api/v1/missions", "/api/v1/actions", "/api/v1/agents", "/api/v1/controllers", "/api/v1/controllers/{controller_id}", "/api/v1/operations", "/api/v1/operations/{operation_id}", "/api/v1/operations/{operation_id}/graph", "/api/v1/operations/{operation_id}/report", "/api/v1/reports/{report_id}", "/api/v1/artifacts/{artifact_id}", "/api/v1/artifacts/{artifact_id}/files/{file_id}"} {
		if document.Paths[path].Get.Responses["200"] == nil {
			t.Errorf("OpenAPI omitted GET %s", path)
		}
	}
}

func assertSchema(t *testing.T, a *api, name string, result response) {
	t.Helper()
	document := read[struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}](a, "/openapi.json")
	data, err := json.Marshal(object{"$defs": document.Components.Schemas, "$ref": "#/$defs/" + name})
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(bytes.ReplaceAll(data, []byte("#/components/schemas/"), []byte("#/$defs/")), &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var body any
	if err := json.Unmarshal([]byte(result.body), &body); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(body); err != nil {
		t.Fatalf("%s response violates published schema: %v", name, err)
	}
}

func assertHeaders(t *testing.T, result response) {
	t.Helper()
	for name, want := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer"} {
		if result.header.Get(name) != want {
			t.Errorf("%s=%q, want %q", name, result.header.Get(name), want)
		}
	}
	if policy := result.header.Get("Content-Security-Policy"); !strings.Contains(policy, "default-src 'none'") || !strings.Contains(policy, "frame-ancestors 'none'") {
		t.Errorf("missing restrictive CSP: %q", policy)
	}
	if result.header.Get("Access-Control-Allow-Origin") != "" || result.header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("API unexpectedly allows cross-origin clients")
	}
}
