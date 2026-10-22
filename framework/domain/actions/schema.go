package actions

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// ResolveSchema compiles an explicit action schema. Actions exchange JSON
// objects, so the root must declare type object. References resolve within the
// document; no external resource loader is configured. Defaults are validated.
func ResolveSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	if document["type"] != "object" {
		return nil, errors.New("schema must declare type object")
	}
	if err := checkSchemaTypes(document); err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
}

// Resolve checks schema structure but accepts unknown type names. Check only
// schema nodes: example/default object values may legitimately have a type key.
func checkSchemaTypes(schema map[string]any) error {
	if value, exists := schema["type"]; exists {
		values, multiple := value.([]any)
		if !multiple {
			values = []any{value}
		}
		if len(values) == 0 {
			return errors.New("schema type list must not be empty")
		}
		seen := map[string]bool{}
		for _, value := range values {
			name, ok := value.(string)
			if !ok || seen[name] {
				return errors.New("schema types must be unique names")
			}
			switch name {
			case "object", "array", "string", "number", "integer", "boolean", "null":
			default:
				return fmt.Errorf("invalid schema type %q", name)
			}
			seen[name] = true
		}
	}
	check := func(value any) error {
		if child, ok := value.(map[string]any); ok {
			return checkSchemaTypes(child)
		}
		return nil
	}
	for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas", "dependencies"} {
		if children, ok := schema[keyword].(map[string]any); ok {
			for _, child := range children {
				if err := check(child); err != nil {
					return err
				}
			}
		}
	}
	for _, keyword := range []string{"items", "additionalItems", "additionalProperties", "contains", "propertyNames", "not", "if", "then", "else", "unevaluatedItems", "unevaluatedProperties", "allOf", "anyOf", "oneOf", "prefixItems", "contentSchema"} {
		if children, ok := schema[keyword].([]any); ok {
			for _, child := range children {
				if err := check(child); err != nil {
					return err
				}
			}
		} else if err := check(schema[keyword]); err != nil {
			return err
		}
	}
	return nil
}
