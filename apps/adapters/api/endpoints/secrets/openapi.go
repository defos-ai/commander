package secrets

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
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
	schema.Description = "Secret request metadata only; never includes secret values, encrypted envelopes or encryption keys."
	schema.Properties["id"].Pattern, schema.Properties["id"].Format = secretIDPattern, "uuid"
	schema.Properties["agent_type"].Pattern = secretAgentTypePattern
	maximum := 63
	schema.Properties["agent_type"].MaxLength = &maximum
	schema.Properties["agent_id"].Pattern = secretRequestAgentIDPattern
	schema.Properties["agent_id"].MaxLength = &maximum
	schema.Properties["name"].Pattern = secretNamePattern
	schema.Properties["type"].Enum = []any{"reusable", "transient"}
	for _, name := range []string{"operation_id", "task_id", "command_id"} {
		property := schema.Properties[name]
		property.Pattern, property.Format = secretIDPattern, "uuid"
		property.Description = "Execution identity for a transient request."
	}
	schema.Properties["status"].Enum = []any{"pending", "needs_value", "awaiting_delivery", "awaiting_installation", "fulfilled", "cancelled", "expired"}
	schema.Properties["status"].Description = "Transient requests use pending: saved request metadata does not reveal whether a value is awaiting input or an in-memory delivery receipt. Reusable requests use needs_value when the managed secret is absent or unavailable, awaiting_delivery without a matching valid reservation, and awaiting_installation when a matching reservation exists without acknowledgement; a reservation does not guarantee publication. Detail reads also expose fulfilled, cancelled and expired. Fulfilled means receipt only, never successful use. An overdue saved pending request projects expired without changing saved state."
	components["SecretRequest"] = schema
	options.TypeSchemas[typeOf] = schema
	operation := endpoints.OpenAPIQueryOperation("listSecretRequests", "List pending secret requests", "Secrets", map[string]any{
		"type": "object", "required": []string{"items"},
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": endpoints.OpenAPIRef("SecretRequest")}},
	})
	operation["description"] = "Returns pending, unexpired reusable and transient requests sorted by request ID, without pagination or changing saved state. Optional filters intersect. Transient requests retain their operation, task and command IDs; their names are never resolved from managed secret storage. Unknown, repeated, empty or malformed query parameters are rejected. This eventually consistent metadata snapshot is not agent readiness or proof that input is still needed. Responses use Cache-Control: no-store."
	operation["parameters"] = []any{
		secretParameter("type", "query", false, map[string]any{"type": "string", "enum": []string{"reusable", "transient"}}),
		secretParameter("agent_type", "query", false, map[string]any{"type": "string", "pattern": secretAgentTypePattern, "maxLength": 63}),
		secretParameter("agent_id", "query", false, map[string]any{"type": "string", "pattern": secretRequestAgentIDPattern, "maxLength": 63}),
	}
	responses := operation["responses"].(map[string]any)
	responses["400"] = endpoints.OpenAPIResponse("invalid_request or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	responses["503"] = endpoints.OpenAPIResponse("secrets_unavailable or request_canceled. Secret management requires configured encryption and a request store.", endpoints.OpenAPIRef("Error"))
	responses["504"] = endpoints.OpenAPIResponse("request_timeout after at most 10 seconds. Reads do not change saved state.", endpoints.OpenAPIRef("Error"))
	paths[secretRequestsPath] = map[string]any{"get": operation}
	detail := endpoints.OpenAPIQueryOperation("getSecretRequest", "Get secret request metadata", "Secrets", endpoints.OpenAPIRef("SecretRequest"))
	detail["description"] = "Returns metadata for one pending or terminal request without exposing values, encrypted envelopes or delivery payloads. Use this read after an uncertain submission. Fulfilled records delivery receipt, not successful use. Overdue pending requests project expired without mutation. Transient names are never resolved from managed secret storage. Reads remain available without a live controller. Query parameters are not accepted. Responses use Cache-Control: no-store."
	detail["parameters"] = []any{
		secretParameter("request_id", "path", true, map[string]any{"type": "string", "format": "uuid", "pattern": secretIDPattern}),
	}
	detailResponses := detail["responses"].(map[string]any)
	detailResponses["400"] = endpoints.OpenAPIResponse("invalid_request or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	detailResponses["404"] = endpoints.OpenAPIResponse("secret_request_not_found", endpoints.OpenAPIRef("Error"))
	detailResponses["503"] = responses["503"]
	detailResponses["504"] = responses["504"]
	paths[secretRequestPath] = map[string]any{"get": detail}
	return registerRequestValueOpenAPI(paths, components, options)
}

const secretRequestAgentIDPattern = `^[a-z0-9]+(-[a-z0-9]+)*-agent-[0-9a-f]{8}$`

func registerRequestValueOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
	for _, model := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"ProvideSecretValueRequest", reflect.TypeFor[models.ProvideSecretValueRequest]()},
		{"SecretValueSubmissionResponse", reflect.TypeFor[models.SecretValueSubmissionResponse]()},
	} {
		schema, err := jsonschema.ForType(model.typeOf, options)
		if err != nil {
			return fmt.Errorf("describe API %s: %w", model.name, err)
		}
		schema.AdditionalProperties = &jsonschema.Schema{Not: &jsonschema.Schema{}}
		if value := schema.Properties["value"]; value != nil {
			value.Type, value.Types, value.AnyOf = "string", nil, nil
			value.WriteOnly = true
			maximum := secretmodel.MaxTransientSecretBytes
			value.MaxLength = &maximum
			value.Description = "Required write-only transient value, at most 64 KiB of UTF-8 bytes. Empty strings are valid; omission and null are invalid. The controller holds it in memory for this request only; it never enters managed secret storage."
			schema.Required = []string{"value"}
		}
		if status := schema.Properties["status"]; status != nil {
			status.Enum = []any{"awaiting_receipt"}
		}
		components[model.name] = schema
		options.TypeSchemas[model.typeOf] = schema
	}
	operation := secretOperation("provideSecretRequestValue", "Provide a transient secret value", endpoints.OpenAPIRef("SecretValueSubmissionResponse"))
	operation["description"] = "Sends a write-only value through the controller for one pending transient request. Requires authentication and enabled secret management. Reusable requests use /api/v1/secrets instead. An identical pending submission retains its delivery identity; changed, closed or expired requests are rejected. Acceptance confirms controller delivery submission, not agent receipt or successful use. No values are returned or stored as managed secrets. Query parameters are not accepted. After an uncertain response, inspect request and operation state before deciding whether to submit again; never request or submit a replacement verification code automatically. Responses use Cache-Control: no-store."
	operation["parameters"] = []any{
		secretParameter("request_id", "path", true, map[string]any{"type": "string", "format": "uuid", "pattern": secretIDPattern}),
	}
	setSecretBody(operation, "ProvideSecretValueRequest")
	responses := operation["responses"].(map[string]any)
	delete(responses, "200")
	responses["202"] = endpoints.OpenAPIResponse("Submitted to the controller; delivery receipt is still pending.", endpoints.OpenAPIRef("SecretValueSubmissionResponse"))
	responses["404"] = endpoints.OpenAPIResponse("secret_request_not_found", endpoints.OpenAPIRef("Error"))
	responses["409"] = endpoints.OpenAPIResponse("secret_request_conflict, secret_request_closed or secret_request_expired", endpoints.OpenAPIRef("Error"))
	responses["503"] = endpoints.OpenAPIResponse("secrets_unavailable, controller_unavailable, secret_delivery_uncertain, secret_write_uncertain or request_canceled. Submission may have succeeded; inspect saved state without automatically resubmitting. No values are returned.", endpoints.OpenAPIRef("Error"))
	responses["504"] = endpoints.OpenAPIResponse("request_timeout after at most 10 seconds. Submission may have succeeded; inspect saved state without automatically resubmitting.", endpoints.OpenAPIRef("Error"))
	paths[secretRequestValuesPath] = map[string]any{"post": operation}
	return nil
}
