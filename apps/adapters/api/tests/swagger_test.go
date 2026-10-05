package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"commander/adapters/api/cli"
	"commander/framework/domain/actions"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	fsoperations "commander/providers/fs/operations"

	"github.com/google/jsonschema-go/jsonschema"
)

var documentationPaths = []string{"/docs", "/docs/", "/docs/swagger-ui.css", "/docs/swagger-ui-bundle.js", "/docs/initializer.js", "/openapi.json"}

func TestSwaggerIsDisabledByDefault(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())

	for _, path := range documentationPaths {
		result := server.request(t, http.MethodGet, path, authenticate)

		if result.status != http.StatusNotFound {
			t.Errorf("disabled documentation %s returned %d", path, result.status)
		}
	}
}

func TestSwaggerServesProtectedLocalUIAndOpenAPI(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_SWAGGER_ENABLED", "true")
	server := startAPI(t, fixture.localArgs())
	server.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	redirect := server.request(t, http.MethodGet, "/docs", authenticate)
	html := server.request(t, http.MethodGet, "/docs/", authenticate)
	spec := server.request(t, http.MethodGet, "/openapi.json", authenticate)

	if redirect.status < 300 || redirect.status >= 400 || redirect.header.Get("Location") != "/docs/" {
		t.Fatalf("unexpected docs redirect: status=%d location=%q", redirect.status, redirect.header.Get("Location"))
	}
	if html.status != http.StatusOK || !strings.Contains(html.header.Get("Content-Type"), "text/html") {
		t.Fatalf("docs did not serve HTML: status=%d content-type=%q", html.status, html.header.Get("Content-Type"))
	}
	assertSecurityHeaders(t, html, false)
	assertSecurityHeaders(t, spec, false)
	if policy := html.header.Get("Content-Security-Policy"); !strings.Contains(policy, "script-src 'self'") || strings.Contains(policy, "unsafe-eval") {
		t.Errorf("Swagger scripts should load locally without unsafe eval: %q", policy)
	}
	if strings.Contains(spec.header.Get("Content-Security-Policy"), "unsafe-inline") {
		t.Error("Swagger HTML policy leaked to API JSON responses")
	}
	for _, path := range documentationPaths {
		assertAPIError(t, server.request(t, http.MethodGet, path), http.StatusUnauthorized, "authentication_required")
	}
	for _, path := range []string{"/docs/swagger-ui.css", "/docs/swagger-ui-bundle.js", "/docs/initializer.js"} {
		if !strings.Contains(html.body, path) {
			t.Errorf("docs HTML does not reference local asset %s", path)
		}
		asset := server.request(t, http.MethodGet, path, authenticate)
		if asset.status != http.StatusOK || len(asset.body) == 0 {
			t.Fatalf("local asset %s failed: status=%d", path, asset.status)
		}
		if path == "/docs/swagger-ui-bundle.js" && !strings.Contains(asset.body, "SwaggerUIBundle") {
			t.Error("docs bundle is not Swagger UI")
		}
		if path == "/docs/initializer.js" && !strings.Contains(asset.body, `supportedSubmitMethods: ["get", "post"]`) {
			t.Error("Swagger UI must allow query and command submission")
		}
	}
	type documentSpec struct {
		OpenAPI  string                `json:"openapi"`
		Security []map[string][]string `json:"security"`
		Paths    map[string]struct {
			Get struct {
				Security  json.RawMessage            `json:"security"`
				Responses map[string]json.RawMessage `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
		Components struct {
			SecuritySchemes map[string]struct {
				Type   string `json:"type"`
				Scheme string `json:"scheme"`
			} `json:"securitySchemes"`
		} `json:"components"`
	}
	document := decodeResponse[documentSpec](t, spec)
	if !strings.HasPrefix(document.OpenAPI, "3.") || len(document.Security) != 1 {
		t.Fatalf("missing OpenAPI version or global security: %+v", document)
	}
	if _, ok := document.Security[0]["basicAuth"]; !ok {
		t.Errorf("global security does not require Basic authentication: %+v", document.Security)
	}
	if scheme := document.Components.SecuritySchemes["basicAuth"]; scheme.Type != "http" || scheme.Scheme != "basic" {
		t.Errorf("incorrect Basic authentication schema: %+v", scheme)
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		if string(document.Paths[path].Get.Security) != "[]" {
			t.Errorf("public probe %s does not override global security", path)
		}
	}
	for _, path := range []string{
		"/api/v1/missions", "/api/v1/missions/{mission_name}", "/api/v1/actions", "/api/v1/actions/{action_name}", "/api/v1/agents", "/api/v1/agents/{agent_id}",
		"/api/v1/operations", "/api/v1/operations/{operation_id}", "/api/v1/operations/{operation_id}/tasks", "/api/v1/operations/{operation_id}/tasks/{task_id}",
		"/api/v1/operations/{operation_id}/events", "/api/v1/operations/{operation_id}/questions", "/api/v1/operations/{operation_id}/questions/{question_id}",
	} {
		endpoint, ok := document.Paths[path]
		if !ok || endpoint.Get.Responses["200"] == nil {
			t.Errorf("OpenAPI does not document GET %s", path)
		}
		if string(endpoint.Get.Security) == "[]" {
			t.Errorf("protected catalog %s declared public", path)
		}
	}
}

func TestSwaggerOperationContractsMatchHTTPResponses(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_SWAGGER_ENABLED", "true")
	store, err := fsoperations.NewOperationFsStore(filepath.Join(fixture.root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := operations.Operation{
		ID: id.OperationID(), Status: operations.Running, CreatedAt: now, UpdatedAt: now,
		Mission: missions.Mission{Name: "inspect", Objective: "Inspect saved state", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)},
		Input:   json.RawMessage(`[1,"two",null]`), Output: json.RawMessage(`true`),
		Preflight: &operations.Preflight{Version: 1, Actions: []operations.ActionReadiness{{
			ActionName: "inspect", UnavailableAgents: []operations.AgentReadiness{{
				AgentID: "web-agent-01234567", Reason: "Missing executable",
				MissingRequirements: []actions.Requirement{{Kind: actions.ExecutableRequirement, Name: "browser"}},
			}},
		}}},
		Questions: []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionPending, Text: "Continue?", AskedAt: now, ExpiresAt: now.Add(time.Hour)}},
	}
	operation.Tasks = []operations.Task{{
		ID: id.TaskID(), OperationID: operation.ID, AgentID: "web-agent-01234567", ActionName: "inspect", Status: operations.TaskSucceeded,
		StartedAt: now, FinishedAt: &now, Input: json.RawMessage(`"saved input"`),
		Result: &actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`42`), Artifacts: []artifacts.Artifact{{
			ID: id.ArtifactID(), Name: "report", AgentID: "web-agent-01234567", ActionName: "inspect", CreatedAt: now,
			Files: []artifacts.ArtifactFile{{ID: id.ArtifactFileID(), Name: "report.txt", MediaType: "text/plain", Size: 0, SHA256: strings.Repeat("0", 64)}},
		}}},
		Continuation: &operations.Continuation{Version: 1, DecisionBy: operations.DecisionModel, Status: operations.ContinuationPending,
			ProposedActions: []actions.NextAction{{ActionName: "follow-up", Input: json.RawMessage(`null`)}}, CreatedTaskIDs: []string{}},
	}}
	operation.AppendEvent(operations.TaskFinished, operation.Tasks[0].ID, "", "Saved result")
	if err := store.Create(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	server := startAPI(t, fixture.localArgs())
	type parameter struct {
		Name     string            `json:"name"`
		In       string            `json:"in"`
		Required bool              `json:"required"`
		Schema   jsonschema.Schema `json:"schema"`
	}
	type responseSpec struct {
		Description string `json:"description"`
		Content     map[string]struct {
			Schema json.RawMessage `json:"schema"`
		} `json:"content"`
	}
	type operationDocument struct {
		Paths map[string]struct {
			Get struct {
				Parameters []parameter             `json:"parameters"`
				Responses  map[string]responseSpec `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]*jsonschema.Schema `json:"schemas"`
		} `json:"components"`
	}
	document := decodeResponse[operationDocument](t, server.request(t, http.MethodGet, "/openapi.json", authenticate))
	for _, model := range []string{"Operation", "Task", "Event", "Question"} {
		if schema := document.Components.Schemas[model]; schema == nil || schema.Type != "object" || len(schema.Properties) == 0 {
			t.Fatalf("missing full %s component schema", model)
		}
	}
	for _, suffix := range []string{"", "/{operation_id}", "/{operation_id}/tasks", "/{operation_id}/tasks/{task_id}", "/{operation_id}/events", "/{operation_id}/questions", "/{operation_id}/questions/{question_id}"} {
		t.Run(suffix, func(t *testing.T) {
			path := "/api/v1/operations" + suffix
			endpoint := document.Paths[path].Get
			for _, parameter := range endpoint.Parameters {
				if parameter.In == "path" && (!parameter.Required || parameter.Schema.Format != "uuid" || parameter.Schema.Pattern == "") {
					t.Errorf("path parameter must require a canonical UUID: %+v", parameter)
				}
			}
			for _, status := range []string{"200", "400", "401", "403", "500", "504"} {
				if _, ok := endpoint.Responses[status]; !ok {
					t.Errorf("missing %s response", status)
				}
			}
			if suffix != "" && !strings.Contains(endpoint.Responses["404"].Description, "operation_not_found") {
				t.Error("missing operation_not_found response")
			}
			if suffix == "/{operation_id}/tasks" || suffix == "/{operation_id}/events" {
				if len(endpoint.Parameters) != 3 || endpoint.Parameters[1].Name != "cursor" || endpoint.Parameters[1].Schema.Type != "string" {
					t.Fatal("page must describe a string cursor and a limit")
				}
				limit := endpoint.Parameters[2]
				if limit.Name != "limit" || limit.Schema.Type != "integer" || limit.Schema.Minimum == nil || *limit.Schema.Minimum != 1 || limit.Schema.Maximum == nil || *limit.Schema.Maximum != 1000 || string(limit.Schema.Default) != "100" {
					t.Errorf("incorrect page limit schema: %+v", limit)
				}
				for _, code := range []string{"invalid_query", "invalid_cursor", "invalid_limit"} {
					if !strings.Contains(endpoint.Responses["400"].Description, code) {
						t.Errorf("missing page error %s", code)
					}
				}
			}
			schemaJSON, err := json.Marshal(map[string]any{"$defs": document.Components.Schemas, "allOf": []any{endpoint.Responses["200"].Content["application/json"].Schema}})
			if err != nil {
				t.Fatal(err)
			}
			var schema jsonschema.Schema
			if err := json.Unmarshal(bytes.ReplaceAll(schemaJSON, []byte("#/components/schemas/"), []byte("#/$defs/")), &schema); err != nil {
				t.Fatal(err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			actualPath := strings.NewReplacer("{operation_id}", operation.ID, "{task_id}", operation.Tasks[0].ID, "{question_id}", operation.Questions[0].ID).Replace(path)
			payload := decodeResponse[any](t, server.request(t, http.MethodGet, actualPath, authenticate))
			if err := resolved.Validate(payload); err != nil {
				t.Errorf("HTTP response does not match documented schema: %v", err)
			}
		})
	}
	questionList := document.Paths["/api/v1/operations/{operation_id}/questions"].Get.Responses["200"].Content["application/json"].Schema
	var questions jsonschema.Schema
	if err := json.Unmarshal(questionList, &questions); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(questions.Required, "last_question") || !slices.Contains(questions.Required, "remaining_questions") || len(questions.Properties["last_question"].AnyOf) != 2 || questions.Properties["last_question"].AnyOf[1].Type != "null" {
		t.Error("question list must require nullable last_question and remaining_questions")
	}
}

func TestSwaggerCommandContracts(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_SWAGGER_ENABLED", "true")
	server := startAPI(t, fixture.localArgs())
	type mediaType struct {
		Schema jsonschema.Schema `json:"schema"`
	}
	type responseSpec struct {
		Description string               `json:"description"`
		Content     map[string]mediaType `json:"content"`
		Headers     map[string]struct {
			Required bool              `json:"required"`
			Schema   jsonschema.Schema `json:"schema"`
		} `json:"headers"`
	}
	type commandSpec struct {
		OperationID string          `json:"operationId"`
		Description string          `json:"description"`
		Security    json.RawMessage `json:"security"`
		RequestBody struct {
			Required    bool                 `json:"required"`
			Description string               `json:"description"`
			Content     map[string]mediaType `json:"content"`
		} `json:"requestBody"`
		Parameters []struct {
			Name     string            `json:"name"`
			In       string            `json:"in"`
			Required bool              `json:"required"`
			Schema   jsonschema.Schema `json:"schema"`
		} `json:"parameters"`
		Responses map[string]responseSpec `json:"responses"`
	}
	type commandDocument struct {
		Paths      map[string]map[string]commandSpec `json:"paths"`
		Components struct {
			Schemas map[string]*jsonschema.Schema `json:"schemas"`
		} `json:"components"`
	}
	document := decodeResponse[commandDocument](t, server.request(t, http.MethodGet, "/openapi.json", authenticate))
	for _, test := range []struct {
		name, suffix, status, model string
		missing, conflicts          []string
		valid, invalid              []string
		invalidInput                bool
	}{
		{
			name: "start", status: "201", model: "Operation", missing: []string{"mission_not_found"}, invalidInput: true,
			valid: []string{
				`{"mission_name":"inspect","input":null}`,
				`{"mission_name":"inspect","input":[1,"two",null]}`,
				`{"mission_name":"inspect","input":true}`,
				`{"mission_name":"inspect","input":42}`,
				`{"mission_name":"inspect","input":"text"}`,
				`{"operation_id":"12345678-1234-1234-1234-123456789abc","mission_name":"inspect","input":{}}`,
			},
			invalid: []string{
				`{"mission_name":"inspect"}`, `{"input":{}}`, `{"mission_name":" \t","input":null}`,
				`{"mission_name":null,"input":{}}`, `{"operation_id":"INVALID","mission_name":"inspect","input":{}}`,
				`{"operation_id":null,"mission_name":"inspect","input":{}}`, `{"mission_name":"inspect","input":{},"extra":true}`,
			},
		},
		{
			name: "assign", suffix: "/{operation_id}/tasks", status: "201", model: "Task", missing: []string{"operation_not_found"},
			conflicts: []string{"question_pending"}, invalidInput: true,
			valid: []string{`{"action_name":"inspect"}`, `{"action_name":"inspect","input":{"any":[1,null]},"step_id":"step"}`},
			invalid: []string{
				`{}`, `{"action_name":""}`, `{"action_name":" \t"}`, `{"action_name":null}`,
				`{"action_name":"inspect","input":null}`, `{"action_name":"inspect","input":[]}`,
				`{"action_name":"inspect","input":"text"}`, `{"action_name":"inspect","step_id":null}`,
				`{"action_name":"inspect","operation_id":"12345678-1234-1234-1234-123456789abc"}`,
				`{"action_name":"inspect","agent_id":"chosen-agent"}`,
			},
		},
		{
			name: "cancel", suffix: "/{operation_id}/cancel", status: "200", model: "Operation", missing: []string{"operation_not_found"},
			valid: []string{`{}`}, invalid: []string{`null`, `[]`, `{"operation_id":"12345678-1234-1234-1234-123456789abc"}`},
		},
		{
			name: "ask", suffix: "/{operation_id}/questions", status: "201", model: "Question",
			missing: []string{"operation_not_found", "task_not_found"}, conflicts: []string{"continuation_conflict"}, invalidInput: true,
			valid: []string{
				`{"text":"Continue?"}`, `{"text":"Continue?","options":[],"timeout_seconds":0}`,
				`{"text":"Continue?","options":["One","Two","Three","Four"],"timeout_seconds":86400,"step_id":"step"}`,
				`{"text":"Continue?","timeout_seconds":1,"task_id":"12345678-1234-1234-1234-123456789abc","version":1}`,
				`{"text":"Continue?","task_id":"12345678-1234-1234-1234-123456789abc","version":18446744073709551615}`,
			},
			invalid: []string{
				`{}`, `null`, `[]`, `{"text":" \t"}`, `{"text":null}`,
				`{"text":"Continue?","options":null}`, `{"text":"Continue?","options":"One"}`,
				`{"text":"Continue?","options":["One","Two","Three","Four","Five"]}`,
				`{"text":"Continue?","options":[""]}`, `{"text":"Continue?","options":[" \t"]}`,
				`{"text":"Continue?","options":[null]}`, `{"text":"Continue?","options":[1]}`,
				`{"text":"Continue?","timeout_seconds":null}`, `{"text":"Continue?","timeout_seconds":-1}`,
				`{"text":"Continue?","timeout_seconds":86401}`, `{"text":"Continue?","timeout_seconds":1.5}`,
				`{"text":"Continue?","timeout_seconds":"120"}`, `{"text":"Continue?","step_id":null}`,
				`{"text":"Continue?","task_id":"12345678-1234-1234-1234-123456789abc"}`, `{"text":"Continue?","version":1}`,
				`{"text":"Continue?","version":0}`, `{"text":"Continue?","task_id":null,"version":1}`,
				`{"text":"Continue?","task_id":"INVALID","version":1}`, `{"text":"Continue?","task_id":"","version":1}`,
				`{"text":"Continue?","task_id":"12345678-1234-1234-1234-123456789abc","version":0}`,
				`{"text":"Continue?","task_id":"12345678-1234-1234-1234-123456789abc","version":null}`,
				`{"text":"Continue?","task_id":"12345678-1234-1234-1234-123456789abc","version":1.5}`,
				`{"text":"Continue?","operation_id":"12345678-1234-1234-1234-123456789abc"}`,
				`{"text":"Continue?","agent_id":"chosen-agent"}`, `{"text":"Continue?","extra":true}`,
			},
		},
		{
			name: "answer", suffix: "/{operation_id}/questions/{question_id}/answer", status: "200", model: "Operation",
			missing: []string{"operation_not_found", "question_not_found"}, conflicts: []string{"question_answered", "question_expired"},
			valid: []string{`{"answer":"Continue"}`},
			invalid: []string{
				`{}`, `null`, `[]`, `{"answer":""}`, `{"answer":" \t"}`, `{"answer":null}`, `{"answer":1}`,
				`{"answer":"Continue","operation_id":"12345678-1234-1234-1234-123456789abc"}`,
				`{"answer":"Continue","question_id":"12345678-1234-1234-1234-123456789abc"}`,
				`{"answer":"Continue","extra":true}`,
			},
		},
		{
			name: "resolve", suffix: "/{operation_id}/tasks/{task_id}/continuation/resolve", status: "200", model: "Operation",
			missing: []string{"operation_not_found", "task_not_found"}, conflicts: []string{"continuation_conflict", "question_pending", "task_limit_exceeded"},
			valid: []string{
				`{"version":1,"actions":[]}`, `{"version":18446744073709551615,"actions":[]}`,
				`{"version":1,"actions":[{"action_name":"inspect"}]}`,
				`{"version":1,"actions":[{"action_name":"inspect","input":{"any":[1,null]},"reason":"Approved"},{"action_name":"report","reason":""}]}`,
			},
			invalid: []string{
				`{}`, `null`, `[]`, `{"version":1}`, `{"actions":[]}`, `{"version":0,"actions":[]}`,
				`{"version":-1,"actions":[]}`, `{"version":null,"actions":[]}`, `{"version":1.5,"actions":[]}`,
				`{"version":"1","actions":[]}`, `{"version":18446744073709560000,"actions":[]}`,
				`{"version":1,"actions":null}`, `{"version":1,"actions":{}}`, `{"version":1,"actions":[null]}`,
				`{"version":1,"actions":[{}]}`, `{"version":1,"actions":[{"action_name":""}]}`,
				`{"version":1,"actions":[{"action_name":" \t"}]}`, `{"version":1,"actions":[{"action_name":null}]}`,
				`{"version":1,"actions":[{"action_name":"inspect","input":null}]}`,
				`{"version":1,"actions":[{"action_name":"inspect","input":[]}]}`,
				`{"version":1,"actions":[{"action_name":"inspect","input":"text"}]}`,
				`{"version":1,"actions":[{"action_name":"inspect","reason":null}]}`,
				`{"version":1,"actions":[{"action_name":"inspect","reason":1}]}`,
				`{"version":1,"actions":[{"action_name":"inspect","agent_id":"chosen-agent"}]}`,
				`{"version":1,"actions":[{"action_name":"inspect","extra":true}]}`,
				`{"version":1,"actions":[],"operation_id":"12345678-1234-1234-1234-123456789abc"}`,
				`{"version":1,"actions":[],"task_id":"12345678-1234-1234-1234-123456789abc"}`,
				`{"version":1,"actions":[],"agent_id":"chosen-agent"}`, `{"version":1,"actions":[],"extra":true}`,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint, ok := document.Paths["/api/v1/operations"+test.suffix]["post"]
			if !ok || endpoint.OperationID == "" {
				t.Fatal("missing command endpoint")
			}
			if len(endpoint.Security) != 0 {
				t.Error("command must inherit global Basic authentication")
			}
			if endpoint.RequestBody.Required != (test.name != "cancel") {
				t.Error("only cancellation may omit the body")
			}
			if len(endpoint.RequestBody.Content) != 1 || !strings.Contains(endpoint.RequestBody.Description, "1 MiB") {
				t.Error("body must document JSON content and the size limit")
			}
			if !strings.Contains(endpoint.Description, "10-second") || !strings.Contains(endpoint.Description, "no automatic retry") || !strings.Contains(endpoint.Description, "query saved state") {
				t.Error("command must explain timeouts and resubmission")
			}
			if len(endpoint.Parameters) != strings.Count(test.suffix, "{") {
				t.Fatal("command must document every path parameter")
			}
			for _, parameter := range endpoint.Parameters {
				if !strings.Contains(test.suffix, "{"+parameter.Name+"}") || parameter.In != "path" || !parameter.Required || parameter.Schema.Format != "uuid" || parameter.Schema.Pattern == "" {
					t.Errorf("incorrect path parameter: %+v", parameter)
				}
				if code := "invalid_" + parameter.Name; !strings.Contains(endpoint.Responses["400"].Description, code) {
					t.Errorf("missing %s response", code)
				}
			}
			for status, codes := range map[string][]string{
				"400": {"invalid_request", "invalid_request_origin"}, "401": {"authentication_required"},
				"403": {"host_not_allowed", "cross_origin_request"}, "404": test.missing, "409": append([]string{"operation_conflict"}, test.conflicts...),
				"413": {"request_too_large"}, "415": {"unsupported_media_type"}, "500": {"internal_error"},
				"503": {"operation_write_uncertain", "command_outcome_unknown", "controller_unavailable", "request_canceled"},
				"504": {"request_timeout"},
			} {
				response := endpoint.Responses[status]
				for _, code := range codes {
					if !strings.Contains(response.Description, code) {
						t.Errorf("missing %s error %s", status, code)
					}
				}
				if response.Content["application/json"].Schema.Ref != "#/components/schemas/Error" {
					t.Errorf("%s response must use the JSON error schema", status)
				}
			}
			if test.invalidInput && !strings.Contains(endpoint.Responses["422"].Description, "invalid_input") {
				t.Error("missing invalid_input response")
			}
			if test.name != "start" && !strings.Contains(endpoint.Responses["409"].Description, "operation_finished") {
				t.Error("missing operation_finished response")
			}
			success := endpoint.Responses[test.status]
			if success.Content["application/json"].Schema.Ref != "#/components/schemas/"+test.model {
				t.Error("success must return the full record schema")
			}
			if test.status == "201" {
				location := success.Headers["Location"]
				if !location.Required || location.Schema.Type != "string" || location.Schema.Format != "uri-reference" {
					t.Error("created response must document the Location header")
				}
				if _, ok := endpoint.Responses["200"]; ok {
					t.Error("created command incorrectly documents 200 success")
				}
			}
			requestRef := endpoint.RequestBody.Content["application/json"].Schema.Ref
			schema := document.Components.Schemas[strings.TrimPrefix(requestRef, "#/components/schemas/")]
			if schema == nil {
				t.Fatal("missing request schema")
			}
			if test.name == "ask" {
				timeout := schema.Properties["timeout_seconds"]
				if timeout == nil || string(timeout.Default) != "120" {
					t.Error("question timeout must document the 120-second default")
				}
			}
			schemaJSON, err := json.Marshal(map[string]any{"$defs": document.Components.Schemas, "$ref": requestRef})
			if err != nil {
				t.Fatal(err)
			}
			var requestSchema jsonschema.Schema
			if err := json.Unmarshal(bytes.ReplaceAll(schemaJSON, []byte("#/components/schemas/"), []byte("#/$defs/")), &requestSchema); err != nil {
				t.Fatal(err)
			}
			resolved, err := requestSchema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, accepted := range []bool{true, false} {
				payloads := test.invalid
				if accepted {
					payloads = test.valid
				}
				for _, raw := range payloads {
					var payload any
					if err := json.Unmarshal([]byte(raw), &payload); err != nil {
						t.Fatal(err)
					}
					if err := resolved.Validate(payload); (err == nil) != accepted {
						t.Errorf("request %s: accepted=%t, validation=%v", raw, accepted, err)
					}
				}
			}
		})
	}
}

func TestSwaggerConfigurationPrecedence(t *testing.T) {
	for _, test := range []struct {
		name, environment, flag string
		status                  int
	}{
		{name: "flag disables environment", environment: "true", flag: "--swagger=false", status: http.StatusNotFound},
		{name: "flag overrides invalid environment", environment: "invalid-private-value", flag: "--swagger=true", status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			t.Setenv("API_SWAGGER_ENABLED", test.environment)
			server := startAPI(t, append(fixture.localArgs(), test.flag))

			result := server.request(t, http.MethodGet, "/openapi.json", authenticate)

			if result.status != test.status {
				t.Fatalf("status=%d, want %d", result.status, test.status)
			}
		})
	}
	t.Run("invalid environment", func(t *testing.T) {
		fixture := newFixture(t)
		t.Setenv("API_SWAGGER_ENABLED", "invalid-private-value")
		var logs bytes.Buffer

		err := cli.Run(t.Context(), fixture.localArgs(), &logs)

		if err == nil || !strings.Contains(err.Error(), "API_SWAGGER_ENABLED") {
			t.Fatalf("invalid boolean did not identify its configuration key: %v", err)
		}
		if strings.Contains(err.Error()+logs.String(), "invalid-private-value") {
			t.Error("invalid environment value leaked to error or logs")
		}
	})
}
