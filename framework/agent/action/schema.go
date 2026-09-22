package action

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// SchemaFor infers a JSON Schema from a Go type. JSON tags name fields;
// omitempty and omitzero make them optional; pointer fields accept null.
// A jsonschema tag supplies a field description. Recursive and unsupported
// types return an error; provide an explicit schema when inference is unsuitable.
func SchemaFor[T any]() (json.RawMessage, error) {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(schema)
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
