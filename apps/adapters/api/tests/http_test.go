package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestReadOnlyQueriesValidateIDsAndEmptyCollections(t *testing.T) {
	a := fixtures.Connect(t)
	op := a.Start(fixtures.FixtureText)
	base := fixtures.OperationPath(op.ID)
	before := a.Request(http.MethodGet, base, nil)
	lastEvent := fmt.Sprint(op.Events[len(op.Events)-1].Sequence)
	for _, tc := range []struct{ path, cursor string }{{base + "/tasks", ""}, {base + "/events?cursor=" + lastEvent, lastEvent}} {
		page := fixtures.Read[map[string]json.RawMessage](a, tc.path)
		if string(page["items"]) != "[]" || string(page["next_cursor"]) != fmt.Sprintf("%q", tc.cursor) {
			t.Fatalf("empty page shape: %+v", page)
		}
	}
	questions := fixtures.Read[models.QuestionListResponse](a, base+"/questions")
	if questions.Items == nil || questions.LastQuestion != nil || questions.RemainingQuestions != 0 {
		t.Fatalf("empty question shape: %+v", questions)
	}
	for _, suffix := range []string{"", "/tasks", "/tasks/" + fixtures.AbsentID, "/events", "/questions", "/questions/" + fixtures.AbsentID, "/graph", "/report"} {
		fixtures.AssertError(t, a.Request(http.MethodGet, fixtures.OperationPath(fixtures.AbsentID)+suffix, nil), http.StatusNotFound, "operation_not_found")
		for _, invalid := range []string{"not-a-uuid", "00000000-0000-4000-8000-000000ABCDEF", strings.ReplaceAll(op.ID, "-", "")} {
			fixtures.AssertError(t, a.Request(http.MethodGet, fixtures.OperationPath(invalid)+suffix, nil), http.StatusBadRequest, "invalid_operation_id")
		}
	}
	for _, tc := range []struct{ path, code string }{
		{base + "/tasks/" + fixtures.AbsentID, "task_not_found"}, {base + "/questions/" + fixtures.AbsentID, "question_not_found"}, {base + "/report", "report_not_found"},
		{"/api/v1/reports/" + fixtures.AbsentID, "report_not_found"}, {"/api/v1/artifacts/" + fixtures.AbsentID, "artifact_not_found"},
	} {
		fixtures.AssertError(t, a.Request(http.MethodGet, tc.path, nil), http.StatusNotFound, tc.code)
	}
	for _, suffix := range []string{"/tasks", "/events"} {
		for _, query := range []string{"limit=0", "limit=1001", "limit=bad"} {
			fixtures.AssertError(t, a.Request(http.MethodGet, base+suffix+"?"+query, nil), http.StatusBadRequest, "invalid_limit")
		}
		for _, query := range []string{"unknown=1", "cursor=&cursor=", "limit=1&limit=2"} {
			fixtures.AssertError(t, a.Request(http.MethodGet, base+suffix+"?"+query, nil), http.StatusBadRequest, "invalid_query")
		}
	}
	fixtures.AssertError(t, a.Request(http.MethodGet, base+"/tasks?cursor=invalid", nil), http.StatusBadRequest, "invalid_cursor")
	fixtures.AssertError(t, a.Request(http.MethodGet, base+"/events?cursor=-1", nil), http.StatusBadRequest, "invalid_cursor")
	fixtures.AssertError(t, a.Request(http.MethodGet, base+"/tasks?cursor="+fixtures.AbsentID, nil), http.StatusNotFound, "task_not_found")
	if after := a.Request(http.MethodGet, base, nil); after.Body != before.Body {
		t.Fatal("read-only/error queries mutated saved operation")
	}
}

func TestHTTPAuthenticationOriginMethodsAndPublicProbes(t *testing.T) {
	a := fixtures.Connect(t)
	op := a.Start(fixtures.FixtureText)
	paths := []string{"/api/v1/missions", "/api/v1/actions", "/api/v1/agents", "/api/v1/controllers", "/api/v1/controllers/unknown-controller", fixtures.OperationPath(op.ID), fixtures.OperationPath(op.ID) + "/graph", "/api/v1/reports/" + fixtures.AbsentID, "/api/v1/artifacts/" + fixtures.AbsentID, "/openapi.json", "/api/v1/unknown"}
	for _, path := range paths {
		unauthorized := a.Request(http.MethodGet, path, nil, func(r *http.Request) { r.Header.Del("Authorization") })
		fixtures.AssertError(t, unauthorized, http.StatusUnauthorized, "authentication_required")
		assertHeaders(t, unauthorized)
		if unauthorized.Header.Get("WWW-Authenticate") != `Basic realm="Commander", charset="UTF-8"` {
			t.Fatal("authentication challenge changed")
		}
		fixtures.AssertError(t, a.Request(http.MethodGet, path, nil, func(r *http.Request) { r.SetBasicAuth(a.Username, "wrong-password") }), http.StatusUnauthorized, "authentication_required")
		fixtures.AssertError(t, a.Request(http.MethodGet, path, nil, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }), http.StatusForbidden, "cross_origin_request")
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if result := a.Request(method, fixtures.OperationPath(op.ID)+"/graph", nil); result.Status != http.StatusMethodNotAllowed {
			t.Fatalf("unsupported method %s: %+v", method, result)
		}
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		result := a.Request(http.MethodGet, path, nil, func(r *http.Request) { r.Header.Del("Authorization") })
		if body := fixtures.Decode[map[string]string](t, result, http.StatusOK); body["status"] != "ok" {
			t.Fatalf("unhealthy probe: %+v", body)
		}
		head := a.Request(http.MethodHead, path, nil, func(r *http.Request) { r.Header.Del("Authorization") })
		if head.Status != http.StatusOK || head.Body != "" {
			t.Fatalf("HEAD probe: %+v", head)
		}
	}
	about := a.Request(http.MethodGet, "/aboutz", nil, func(r *http.Request) { r.Header.Del("Authorization") })
	deployment := fixtures.Decode[map[string]string](t, about, http.StatusOK)
	if len(deployment) != 2 || deployment["runtime"] != "docker-compose" || deployment["application"] != "commander-api" {
		t.Fatalf("unexpected public deployment metadata: %+v", deployment)
	}
	assertHeaders(t, about)
	assertSchema(t, a, "About", about)
	fixtures.AssertError(t, a.Request(http.MethodPost, "/api/v1/testing/cleanup", nil, func(r *http.Request) { r.Header.Del("Authorization") }), http.StatusUnauthorized, "authentication_required")
	fixtures.AssertError(t, a.Request(http.MethodPost, "/api/v1/testing/cleanup", nil, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }), http.StatusForbidden, "cross_origin_request")
	if result := a.Request(http.MethodGet, "/api/v1/testing/cleanup", nil); result.Status != http.StatusMethodNotAllowed {
		t.Fatalf("cleanup must require POST: %+v", result)
	}
}

func TestSwaggerServesProtectedLocalAssetsAndOperationContracts(t *testing.T) {
	a := fixtures.Connect(t)
	for _, path := range []string{"/docs/", "/docs/swagger-ui.css", "/docs/swagger-ui-bundle.js", "/docs/initializer.js", "/openapi.json"} {
		result := a.Request(http.MethodGet, path, nil)
		if result.Status != http.StatusOK || result.Body == "" {
			t.Fatalf("Swagger resource %s: %+v", path, result)
		}
		fixtures.AssertError(t, a.Request(http.MethodGet, path, nil, func(r *http.Request) { r.Header.Del("Authorization") }), http.StatusUnauthorized, "authentication_required")
	}
	document := fixtures.Read[struct {
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
	rule := document.Components.Schemas["MissionRule"]
	if rule.Properties["for_actions"] == nil || rule.Properties["actions"] != nil {
		t.Fatal("OpenAPI must identify rule selectors as for_actions")
	}
	params := document.Components.Schemas["MissionRuleParams"]
	if decision := params.Properties["decision_by"]; decision == nil || len(decision.Enum) != 1 || decision.Enum[0] != "model" {
		t.Fatal("OpenAPI must document model as the supported rule decision maker")
	}
	controller := document.Components.Schemas["Controller"]
	for _, name := range []string{"agent_selector", "rule_selector"} {
		if property := controller.Properties[name]; property == nil || property.Type != "string" || !slices.Contains(controller.Required, name) {
			t.Fatalf("OpenAPI must publish controller %s as a required string", name)
		}
	}
	if document.Components.Schemas["Action"].Properties["required_permissions"] != nil || document.Components.Schemas["Agent"].Properties["granted_permissions"] != nil {
		t.Fatal("OpenAPI must not publish application action permissions")
	}
	requirement := document.Components.Schemas["ActionRequirement"]
	if kind := requirement.Properties["kind"]; kind == nil || len(kind.Enum) != 3 || kind.Enum[0] != "executable" || kind.Enum[1] != "configuration" || kind.Enum[2] != "secret" {
		t.Fatal("OpenAPI must describe executable, configuration and managed secret requirements")
	}
	for _, path := range []string{"/api/v1/missions", "/api/v1/actions", "/api/v1/agents", "/api/v1/controllers", "/api/v1/controllers/{controller_id}", "/api/v1/operations", "/api/v1/operations/{operation_id}", "/api/v1/operations/{operation_id}/graph", "/api/v1/operations/{operation_id}/report", "/api/v1/reports/{report_id}", "/api/v1/artifacts/{artifact_id}", "/api/v1/artifacts/{artifact_id}/files/{file_id}"} {
		if document.Paths[path].Get.Responses["200"] == nil {
			t.Errorf("OpenAPI omitted GET %s", path)
		}
	}
}

func assertSchema(t *testing.T, a *fixtures.Client, name string, result fixtures.Response) {
	t.Helper()
	document := fixtures.Read[struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}](a, "/openapi.json")
	data, err := json.Marshal(fixtures.Object{"$defs": document.Components.Schemas, "$ref": "#/$defs/" + name})
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
	if err := json.Unmarshal([]byte(result.Body), &body); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(body); err != nil {
		t.Fatalf("%s response violates published schema: %v", name, err)
	}
}

func assertHeaders(t *testing.T, result fixtures.Response) {
	t.Helper()
	for name, want := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer"} {
		if result.Header.Get(name) != want {
			t.Errorf("%s=%q, want %q", name, result.Header.Get(name), want)
		}
	}
	if policy := result.Header.Get("Content-Security-Policy"); !strings.Contains(policy, "default-src 'none'") || !strings.Contains(policy, "frame-ancestors 'none'") {
		t.Errorf("missing restrictive CSP: %q", policy)
	}
	if result.Header.Get("Access-Control-Allow-Origin") != "" || result.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("API unexpectedly allows cross-origin clients")
	}
}
