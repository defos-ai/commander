package action

import (
	"encoding/json"

	"github.com/defos-ai/commander/framework/domain/actions"

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

// ResolveSchema compiles an explicit object schema using the shared action
// contract validator, including local references and validated defaults.
func ResolveSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	return actions.ResolveSchema(raw)
}
