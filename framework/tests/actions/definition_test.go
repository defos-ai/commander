package actions_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/domain/actions"
)

func TestDefinitionValidatesStaticContractWithoutReadiness(t *testing.T) {
	definition := actions.Definition{Name: "build-agent", RequiredSecrets: []string{"OPENAI_API_KEY"},
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	if err := definition.Validate(); err != nil {
		t.Fatalf("empty description and unconstrained schemas must remain valid: %v", err)
	}
	for _, test := range []struct {
		name   string
		change func(*actions.Definition)
	}{
		{"blank name", func(value *actions.Definition) { value.Name = " \t" }},
		{"invalid secret name", func(value *actions.Definition) { value.RequiredSecrets = []string{"../TOKEN"} }},
		{"duplicate secret", func(value *actions.Definition) { value.RequiredSecrets = []string{"TOKEN", "TOKEN"} }},
		{"array schema", func(value *actions.Definition) { value.InputSchema = json.RawMessage(`[]`) }},
		{"null schema", func(value *actions.Definition) { value.OutputSchema = json.RawMessage(`null`) }},
		{"malformed schema", func(value *actions.Definition) { value.OutputSchema = json.RawMessage(`{`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := definition.Clone()
			test.change(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("invalid static contract accepted")
			}
		})
	}
	withoutSchemas := definition.Clone()
	withoutSchemas.InputSchema, withoutSchemas.OutputSchema = nil, nil
	if err := withoutSchemas.ValidateRequiredSecrets(); err != nil {
		t.Fatalf("secret validation must work before schema inference: %v", err)
	}
	runtime := actions.Action{Name: definition.Name, RequiredSecrets: definition.RequiredSecrets,
		InputSchema: definition.InputSchema, OutputSchema: definition.OutputSchema, Status: actions.Ready,
		MissingRequirements: []actions.Requirement{{Kind: actions.SecretRequirement, Name: "OPENAI_API_KEY"}}}
	if err := runtime.Definition().Validate(); err != nil {
		t.Fatalf("runtime blockers must not invalidate the static contract: %v", err)
	}
	if err := runtime.Validate(); err == nil {
		t.Fatal("ready runtime action accepted missing requirements")
	}
}

func TestDefinitionSnapshotsOwnSchemasAndSecretNames(t *testing.T) {
	runtime := actions.Action{Name: "build-agent", RequiredSecrets: []string{"OPENAI_API_KEY"},
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}
	definition := runtime.Definition()
	clone := definition.Clone()
	clone.RequiredSecrets[0] = "GITHUB_TOKEN"
	clone.InputSchema[0], clone.OutputSchema[0] = '[', '['
	if definition.RequiredSecrets[0] != "OPENAI_API_KEY" || string(definition.InputSchema) != `{"type":"object"}` || string(definition.OutputSchema) != `{"type":"object"}` {
		t.Fatal("mutating a cloned definition changed its source")
	}
	definition.RequiredSecrets[0] = "GITHUB_TOKEN"
	definition.InputSchema[0], definition.OutputSchema[0] = '[', '['
	if runtime.RequiredSecrets[0] != "OPENAI_API_KEY" || string(runtime.InputSchema) != `{"type":"object"}` || string(runtime.OutputSchema) != `{"type":"object"}` {
		t.Fatal("mutating a definition projection changed the runtime action")
	}
}

func TestDefinitionJSONSeparatesStaticMetadataFromFlatRuntimeAction(t *testing.T) {
	raw := []byte(`{"name":"build-agent","description":"Build an agent","status":"unavailable","status_reason":"Requires OPENAI_API_KEY","missing_requirements":[{"kind":"secret","name":"OPENAI_API_KEY"}],"required_secrets":["OPENAI_API_KEY"],"input_schema":{"type":"object"},"output_schema":{}}`)
	var runtime actions.Action
	if err := json.Unmarshal(raw, &runtime); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	var original, roundTrip map[string]any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, roundTrip) {
		t.Fatalf("runtime action wire format changed: %s", encoded)
	}
	encoded, err = json.Marshal(runtime.Definition())
	if err != nil {
		t.Fatal(err)
	}
	var static map[string]any
	if err := json.Unmarshal(encoded, &static); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"status", "status_reason", "missing_requirements"} {
		delete(original, field)
	}
	if !reflect.DeepEqual(original, static) {
		t.Fatalf("static definition must contain only the action contract: %s", encoded)
	}
}
