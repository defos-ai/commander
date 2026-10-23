package api_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestMissionCreationOpenAPIContract(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, append(fixture.localArgs(), "--swagger"))
	document := decodeResponse[struct {
		Security []map[string][]string `json:"security"`
		Paths    map[string]map[string]struct {
			OperationID string          `json:"operationId"`
			Security    json.RawMessage `json:"security"`
			RequestBody struct {
				Required bool `json:"required"`
				Content  map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
			Responses map[string]struct {
				Description string `json:"description"`
				Content     map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
				Headers map[string]struct {
					Required bool `json:"required"`
				} `json:"headers"`
			} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}](t, server.request(t, http.MethodGet, "/openapi.json", authenticate))
	create := document.Paths["/api/v1/missions"]["post"]
	if create.OperationID != "createMission" || !create.RequestBody.Required || create.RequestBody.Content["application/json"].Schema.Ref != "#/components/schemas/CreateMissionRequest" {
		t.Fatal("mission creation must document its required JSON request")
	}
	if !reflect.DeepEqual(document.Security, []map[string][]string{{"basicAuth": {}}}) || len(create.Security) != 0 {
		t.Fatal("mission creation must inherit Basic authentication")
	}
	if create.Responses["201"].Content["application/json"].Schema.Ref != "#/components/schemas/Mission" || !create.Responses["201"].Headers["Location"].Required {
		t.Fatal("mission creation must return a mission and required Location header")
	}
	if _, present := create.Responses["200"]; present {
		t.Error("mission creation must document 201 instead of 200")
	}
	for status, code := range map[string]string{
		"400": "invalid_request", "401": "authentication_required", "403": "host_not_allowed",
		"409": "mission_exists", "413": "request_too_large", "415": "unsupported_media_type",
		"500": "internal_error", "503": "request_canceled", "504": "request_timeout",
	} {
		if !strings.Contains(create.Responses[status].Description, code) {
			t.Errorf("mission creation does not document %s %s", status, code)
		}
	}
	for _, status := range []string{"503", "504"} {
		if !strings.Contains(create.Responses[status].Description, "before retrying") {
			t.Errorf("%s must explain how to reconcile uncertain creation", status)
		}
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(document.Components.Schemas["CreateMissionRequest"], &schema); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(schema.Required, []string{"name", "objectives", "input_schema", "output_schema"}) {
		t.Fatalf("unexpected required mission fields: %v", schema.Required)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, fields, missionName, objectives string
		valid                                 bool
	}{
		{name: "minimal", valid: true},
		{name: "multiple objectives", objectives: `["Build a useful agent","Verify its action contracts"]`, valid: true},
		{name: "string objectives", objectives: `"Build a useful agent"`},
		{name: "null objectives", objectives: `null`},
		{name: "empty objectives", objectives: `[]`},
		{name: "blank objective item", objectives: `["Build a useful agent"," "]`},
		{name: "legacy objective", fields: `,"objective":"Build a useful agent"`},
		{name: "dot name", missionName: "."},
		{name: "parent name", missionName: ".."},
		{name: "uninstalled actions", fields: `,"required_actions":["future-action"],"starting_actions":["future-action"]`, valid: true},
		{name: "nullable omitted collections", fields: `,"required_actions":null,"optional_actions":null,"starting_actions":null,"steps":null,"rules":null,"action_definitions":null`, valid: true},
		{name: "action definition", fields: `,"required_actions":["build"],"action_definitions":[{"name":"build","description":"Build an agent","input_schema":{"type":"object","x-extra":true},"output_schema":{"type":"object"},"required_secrets":["CODEX_API_KEY"]}]`, valid: true},
		{name: "minimal action definition", fields: `,"required_actions":["build"],"action_definitions":[{"name":"build","input_schema":{"type":"object"},"output_schema":{"type":"object"},"required_secrets":null}]`, valid: true},
		{name: "unknown action definition field", fields: `,"action_definitions":[{"name":"build","input_schema":{"type":"object"},"output_schema":{"type":"object"},"status":"ready"}]`},
		{name: "missing action schema type", fields: `,"action_definitions":[{"name":"build","input_schema":{},"output_schema":{"type":"object"}}]`},
		{name: "nonobject action schema type", fields: `,"action_definitions":[{"name":"build","input_schema":{"type":"array"},"output_schema":{"type":"object"}}]`},
		{name: "padded action definition name", fields: `,"action_definitions":[{"name":" build ","input_schema":{"type":"object"},"output_schema":{"type":"object"}}]`},
		{name: "duplicate action secret", fields: `,"action_definitions":[{"name":"build","input_schema":{"type":"object"},"output_schema":{"type":"object"},"required_secrets":["TOKEN","TOKEN"]}]`},
		{name: "invalid action secret name", fields: `,"action_definitions":[{"name":"build","input_schema":{"type":"object"},"output_schema":{"type":"object"},"required_secrets":["bad secret"]}]`},
		{name: "all rule types", fields: `,"rules":{"aggregate-results":{"for_actions":["batch"]},"decide-next-actions":{"for_actions":["check"],"params":{"decision_by":"model"}},"same-agent":{"for_actions":["build"]}}`, valid: true},
		{name: "empty nondecision parameter", fields: `,"rules":{"same-agent":{"for_actions":["build"],"params":{"decision_by":null}}}`, valid: true},
		{name: "step guidance", fields: `,"steps":[{"id":"build","objective":"Build it","agent_types":null,"required_actions":null}]`, valid: true},
		{name: "unknown field", fields: `,"automations":{}`},
		{name: "unknown step field", fields: `,"steps":[{"id":"build","objective":"Build it","unknown":true}]`},
		{name: "unknown rule", fields: `,"rules":{"unknown":{"for_actions":["build"]}}`},
		{name: "unknown rule field", fields: `,"rules":{"same-agent":{"for_actions":["build"],"unknown":true}}`},
		{name: "unknown rule parameter", fields: `,"rules":{"decide-next-actions":{"for_actions":["build"],"params":{"decision_by":"model","unknown":true}}}`},
		{name: "empty rule actions", fields: `,"rules":{"same-agent":{"for_actions":[]}}`},
		{name: "null rule actions", fields: `,"rules":{"same-agent":{"for_actions":null}}`},
		{name: "duplicate action", fields: `,"required_actions":["build","build"]`},
		{name: "padded action", fields: `,"required_actions":[" build "]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(`{"name":"new-mission","objectives":["Build a useful agent"],"input_schema":{},"output_schema":{}`+test.fields+`}`), &value); err != nil {
				t.Fatal(err)
			}
			if test.missionName != "" {
				value.(map[string]any)["name"] = test.missionName
			}
			if test.objectives != "" {
				var objectives any
				if err := json.Unmarshal([]byte(test.objectives), &objectives); err != nil {
					t.Fatal(err)
				}
				value.(map[string]any)["objectives"] = objectives
			}
			if err := resolved.Validate(value); (err == nil) != test.valid {
				t.Fatalf("schema validation=%v, want valid=%v", err, test.valid)
			}
		})
	}
}
