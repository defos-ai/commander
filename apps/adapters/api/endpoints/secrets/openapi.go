package secrets

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/google/jsonschema-go/jsonschema"
)

const secretAgentTypePattern = `^[a-z0-9]+(-[a-z0-9]+)*$`
const secretNamePattern = `^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`
const secretIDPattern = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`

func RegisterOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
	for _, model := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"Secret", reflect.TypeFor[models.SecretResponse]()},
		{"CreateSecretRequest", reflect.TypeFor[models.CreateSecretRequest]()},
		{"ReplaceSecretRequest", reflect.TypeFor[models.ReplaceSecretRequest]()},
	} {
		schema, err := jsonschema.ForType(model.typeOf, options)
		if err != nil {
			return fmt.Errorf("describe API %s: %w", model.name, err)
		}
		constrainSecretSchema(schema, model.name)
		data, err := json.Marshal(schema)
		if err != nil {
			return fmt.Errorf("encode API %s schema: %w", model.name, err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("decode API %s schema: %w", model.name, err)
		}
		// JSON Schema's Go type uses float64 for numeric bounds; preserve the
		// exact uint64 limit in the published document.
		if version, ok := document["properties"].(map[string]any)["version"].(map[string]any); ok {
			version["maximum"] = ^uint64(0)
		}
		components[model.name] = document
		options.TypeSchemas[model.typeOf] = schema
	}
	registerSecretPaths(paths)
	return registerRequestsOpenAPI(paths, components, options)
}

func constrainSecretSchema(schema *jsonschema.Schema, model string) {
	if property := schema.Properties["name"]; property != nil {
		property.Pattern = secretNamePattern
	}
	if property := schema.Properties["id"]; property != nil {
		property.Pattern, property.Format = secretIDPattern, "uuid"
	}
	if property := schema.Properties["version"]; property != nil {
		minimum := float64(1)
		property.Minimum = &minimum
		if model == "ReplaceSecretRequest" {
			property.Description = "Current saved version to replace. A successful replacement increments it by one."
		}
	}
	if model == "Secret" {
		schema.Description = "Metadata only; secret values, encrypted envelopes and encryption keys are never returned."
		schema.Properties["status"].Enum = []any{"available", "expired"}
		if property := schema.Properties["expires_at"]; property != nil {
			property.Type, property.Types, property.AnyOf = "string", nil, nil
		}
		return
	}
	schema.AdditionalProperties = &jsonschema.Schema{Not: &jsonschema.Schema{}}
	value := schema.Properties["value"]
	value.Type, value.Types, value.AnyOf = "string", nil, nil
	value.WriteOnly = true
	value.Description = "Required secret value. Empty strings are valid; omission and null are invalid. Sent only over an authenticated connection and encrypted before storage."
	schema.Properties["expires_at"].Description = "Optional RFC 3339 timestamp strictly after the update time. Omitted or null means no expiry; on replacement this removes any previous expiry."
}

func registerSecretPaths(paths map[string]any) {
	list := secretOperation("listSecrets", "List secret metadata", map[string]any{
		"type": "object", "required": []string{"items"},
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": endpoints.OpenAPIRef("Secret")}},
	})
	list["description"] = "Returns shared secret metadata sorted by name, including expired secrets. Reads project status without changing stored state. No values are returned. Query parameters are not accepted."
	create := secretOperation("createSecret", "Create a secret", endpoints.OpenAPIRef("Secret"))
	create["description"] = "Encrypts a new shared secret identified by name, independent of agents and operations. The server assigns the ID and version 1. Existing names return secret_conflict. Returns metadata only. Query parameters are not accepted."
	setSecretBody(create, "CreateSecretRequest")
	responses := create["responses"].(map[string]any)
	delete(responses, "200")
	success := endpoints.OpenAPIResponse("Created metadata", endpoints.OpenAPIRef("Secret"))
	success["headers"] = map[string]any{"Location": map[string]any{
		"description": "Relative URL of the created secret metadata.", "required": true,
		"schema": map[string]any{"type": "string", "format": "uri-reference"},
	}}
	responses["201"] = success
	paths[secretsPath] = map[string]any{"get": list, "post": create}

	get := secretOperation("getSecret", "Get secret metadata", endpoints.OpenAPIRef("Secret"))
	get["description"] = "Returns metadata and current availability for the shared secret name. No values are returned. Query parameters are not accepted."
	get["responses"].(map[string]any)["404"] = endpoints.OpenAPIResponse("secret_not_found", endpoints.OpenAPIRef("Error"))
	replace := secretOperation("replaceSecret", "Replace a secret", endpoints.OpenAPIRef("Secret"))
	replace["description"] = "Replaces the value and expiry only when the saved ID and version match. Preserves identity and creation time, and increments version. The body must contain a new value. Omitting expires_at removes any previous expiry. A deleted and recreated key has a new ID, so a stale replacement cannot change it. Returns metadata only. Query parameters are not accepted."
	setSecretBody(replace, "ReplaceSecretRequest")
	replace["responses"].(map[string]any)["404"] = endpoints.OpenAPIResponse("secret_not_found", endpoints.OpenAPIRef("Error"))
	remove := secretOperation("deleteSecret", "Delete a secret", nil)
	remove["description"] = "Deletes only the specified saved ID and version. A stale ID or version conflicts with a replacement or recreated key. Deletion of an absent key retires the supplied ID and succeeds, preventing delayed creation with that identity. Unknown or repeated query parameters and malformed query encoding are rejected."
	responses = remove["responses"].(map[string]any)
	delete(responses, "200")
	responses["204"] = map[string]any{"description": "Secret deleted or supplied identity already absent."}
	responses["409"] = endpoints.OpenAPIResponse("secret_conflict", endpoints.OpenAPIRef("Error"))
	for _, route := range []map[string]any{get, replace, remove} {
		route["parameters"] = []any{
			secretParameter("name", "path", true, map[string]any{
				"type": "string", "pattern": secretNamePattern,
			}),
		}
	}
	remove["parameters"] = append(remove["parameters"].([]any),
		secretParameter("id", "query", true, map[string]any{"type": "string", "format": "uuid", "pattern": secretIDPattern}),
		secretParameter("version", "query", true, map[string]any{"type": "integer", "minimum": 1, "maximum": ^uint64(0)}),
	)
	paths[secretPath] = map[string]any{"get": get, "put": replace, "delete": remove}
}

func secretOperation(id, summary string, schema any) map[string]any {
	operation := endpoints.OpenAPIQueryOperation(id, summary, "Secrets", schema)
	responses := operation["responses"].(map[string]any)
	responses["400"] = endpoints.OpenAPIResponse("invalid_request or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	responses["503"] = endpoints.OpenAPIResponse("secrets_unavailable, secret_write_uncertain or request_canceled. Secret management requires configured encryption. An uncertain write takes precedence over a simultaneous timeout; read metadata to reconcile before retrying.", endpoints.OpenAPIRef("Error"))
	responses["504"] = endpoints.OpenAPIResponse("request_timeout after at most 10 seconds. A write may have succeeded; read metadata before retrying. Requests are never retried automatically.", endpoints.OpenAPIRef("Error"))
	return operation
}

func setSecretBody(operation map[string]any, schema string) {
	operation["requestBody"] = map[string]any{
		"required":    true,
		"description": "At most 1 MiB. Requires application/json; charset parameters are accepted. Unknown fields, invalid JSON or invalid values return invalid_request. Secret values are write-only.",
		"content":     map[string]any{"application/json": map[string]any{"schema": endpoints.OpenAPIRef(schema)}},
	}
	responses := operation["responses"].(map[string]any)
	responses["409"] = endpoints.OpenAPIResponse("secret_conflict", endpoints.OpenAPIRef("Error"))
	responses["413"] = endpoints.OpenAPIResponse("request_too_large", endpoints.OpenAPIRef("Error"))
	responses["415"] = endpoints.OpenAPIResponse("unsupported_media_type", endpoints.OpenAPIRef("Error"))
}

func secretParameter(name, location string, required bool, schema any) map[string]any {
	return map[string]any{"name": name, "in": location, "required": required, "schema": schema}
}

func registerRequestsOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
	typeOf := reflect.TypeFor[models.SecretRequestResponse]()
	schema, err := jsonschema.ForType(typeOf, options)
	if err != nil {
		return fmt.Errorf("describe API SecretRequest: %w", err)
	}
	schema.Description = "Pending request metadata only; never includes secret values, encrypted envelopes or encryption keys."
	schema.Properties["id"].Pattern, schema.Properties["id"].Format = secretIDPattern, "uuid"
	schema.Properties["agent_type"].Pattern = secretAgentTypePattern
	maximum := 63
	schema.Properties["agent_type"].MaxLength = &maximum
	schema.Properties["agent_id"].Pattern = secretRequestAgentIDPattern
	schema.Properties["agent_id"].MaxLength = &maximum
	schema.Properties["name"].Pattern = secretNamePattern
	schema.Properties["status"].Enum = []any{"needs_value", "awaiting_delivery", "awaiting_installation"}
	schema.Properties["status"].Description = "needs_value: the current secret is absent or unavailable. awaiting_delivery: a current value exists without a matching valid reserved delivery. awaiting_installation: a matching delivery is reserved, but installation has not been acknowledged; publication is not guaranteed."
	components["SecretRequest"] = schema
	options.TypeSchemas[typeOf] = schema
	operation := endpoints.OpenAPIQueryOperation("listSecretRequests", "List pending secret requests", "Secrets", map[string]any{
		"type": "object", "required": []string{"items"},
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": endpoints.OpenAPIRef("SecretRequest")}},
	})
	operation["description"] = "Returns pending, unexpired requests sorted by request ID, without pagination or changing saved state. Optional filters intersect. Unknown, repeated, empty or malformed query parameters are rejected. This is an eventually consistent snapshot across request and secret stores, not agent readiness: lease refreshes can overlap for one agent and key. Responses use Cache-Control: no-store."
	operation["parameters"] = []any{
		secretParameter("agent_type", "query", false, map[string]any{"type": "string", "pattern": secretAgentTypePattern, "maxLength": 63}),
		secretParameter("agent_id", "query", false, map[string]any{"type": "string", "pattern": secretRequestAgentIDPattern, "maxLength": 63}),
	}
	responses := operation["responses"].(map[string]any)
	responses["400"] = endpoints.OpenAPIResponse("invalid_request or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	responses["503"] = endpoints.OpenAPIResponse("secrets_unavailable or request_canceled. Secret management requires configured encryption and a request store.", endpoints.OpenAPIRef("Error"))
	responses["504"] = endpoints.OpenAPIResponse("request_timeout after at most 10 seconds. Reads do not change saved state.", endpoints.OpenAPIRef("Error"))
	paths[secretRequestsPath] = map[string]any{"get": operation}
	return nil
}

const secretRequestAgentIDPattern = `^[a-z0-9]+(-[a-z0-9]+)*-agent-[0-9a-f]{8}$`
