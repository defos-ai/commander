package endpoints

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
	"github.com/google/jsonschema-go/jsonschema"
)

const secretsPath = "/api/v1/secrets"
const secretPath = secretsPath + "/{agent_type}/{name}"

func RegisterSecrets(mux *http.ServeMux, service *secretservice.Service) {
	handlers := &secretHandler{service: service}
	for _, route := range []struct {
		pattern string
		handle  http.HandlerFunc
	}{
		{"GET " + secretsPath, handlers.list},
		{"POST " + secretsPath, handlers.create},
		{"GET " + secretPath, handlers.get},
		{"PUT " + secretPath, handlers.replace},
		{"DELETE " + secretPath, handlers.delete},
	} {
		mux.HandleFunc(route.pattern, RequestHandler(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if service == nil {
				WriteError(w, http.StatusServiceUnavailable, "secrets_unavailable")
				return
			}
			route.handle(w, r)
		}))
	}
}

type secretHandler struct {
	service *secretservice.Service
}

func (h *secretHandler) list(w http.ResponseWriter, r *http.Request) {
	query, ok := readSecretQuery(w, r, "agent_type")
	if !ok {
		return
	}
	scope, err := secretmodel.NewScope(query.Get("agent_type"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	items, err := h.service.List(r.Context(), scope)
	if writeSecretError(w, err) {
		return
	}
	WriteJSON(w, http.StatusOK, ListResponse(mappings.ToSecretResponses(items, time.Now())))
}

func (h *secretHandler) create(w http.ResponseWriter, r *http.Request) {
	if _, ok := readSecretQuery(w, r); !ok {
		return
	}
	var request models.CreateSecretRequest
	if !ReadJSON(w, r, &request, false) {
		return
	}
	scope, err := secretmodel.NewScope(request.AgentType)
	key := secretmodel.Key{Scope: scope, Name: request.Name}
	if err != nil || key.Validate() != nil || request.Value == nil || (request.ExpiresAt != nil && request.ExpiresAt.IsZero()) {
		WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	metadata, err := h.service.Create(r.Context(), key, []byte(*request.Value), secretExpiration(request.ExpiresAt))
	if writeSecretError(w, err) {
		return
	}
	w.Header().Set("Location", secretsPath+"/"+key.Scope.AgentType+"/"+key.Name)
	WriteJSON(w, http.StatusCreated, mappings.ToSecretResponse(metadata, time.Now()))
}

func (h *secretHandler) get(w http.ResponseWriter, r *http.Request) {
	if _, ok := readSecretQuery(w, r); !ok {
		return
	}
	key, ok := readSecretKey(w, r)
	if !ok {
		return
	}
	metadata, err := h.service.GetMetadata(r.Context(), key)
	if writeSecretError(w, err) {
		return
	}
	WriteJSON(w, http.StatusOK, mappings.ToSecretResponse(metadata, time.Now()))
}

func (h *secretHandler) replace(w http.ResponseWriter, r *http.Request) {
	if _, ok := readSecretQuery(w, r); !ok {
		return
	}
	key, ok := readSecretKey(w, r)
	if !ok {
		return
	}
	var request models.ReplaceSecretRequest
	if !ReadJSON(w, r, &request, false) {
		return
	}
	if !id.Valid(request.ID) || request.Version == 0 || request.Value == nil || (request.ExpiresAt != nil && request.ExpiresAt.IsZero()) {
		WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	metadata, err := h.service.Replace(r.Context(), key, request.ID, request.Version, []byte(*request.Value), secretExpiration(request.ExpiresAt))
	if writeSecretError(w, err) {
		return
	}
	WriteJSON(w, http.StatusOK, mappings.ToSecretResponse(metadata, time.Now()))
}

func (h *secretHandler) delete(w http.ResponseWriter, r *http.Request) {
	query, ok := readSecretQuery(w, r, "id", "version")
	if !ok {
		return
	}
	key, ok := readSecretKey(w, r)
	if !ok {
		return
	}
	version, err := strconv.ParseUint(query.Get("version"), 10, 64)
	if err != nil || version == 0 || !id.Valid(query.Get("id")) {
		WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if writeSecretError(w, h.service.Delete(r.Context(), key, query.Get("id"), version)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readSecretQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	}
	for name, values := range query {
		if !slices.Contains(allowed, name) || len(values) != 1 {
			WriteError(w, http.StatusBadRequest, "invalid_request")
			return nil, false
		}
	}
	return query, true
}

func readSecretKey(w http.ResponseWriter, r *http.Request) (secretmodel.Key, bool) {
	key := secretmodel.Key{
		Scope: secretmodel.Scope{AgentType: r.PathValue("agent_type")},
		Name:  r.PathValue("name"),
	}
	if key.Validate() != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request")
		return secretmodel.Key{}, false
	}
	return key, true
}

func secretExpiration(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func writeSecretError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, secretmodel.ErrWriteUncertain):
		status, code = http.StatusServiceUnavailable, "secret_write_uncertain"
	case errors.Is(err, domain.ErrInvalidInput):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, secretmodel.ErrUnknownSecret):
		status, code = http.StatusNotFound, "secret_not_found"
	case errors.Is(err, secretmodel.ErrSecretConflict):
		status, code = http.StatusConflict, "secret_conflict"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "request_timeout"
	case errors.Is(err, context.Canceled):
		status, code = http.StatusServiceUnavailable, "request_canceled"
	}
	WriteError(w, status, code)
	return true
}

const secretScopePattern = `^[a-z0-9]+(-[a-z0-9]+)*$`
const secretNamePattern = `^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`
const secretIDPattern = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`

func RegisterSecretsOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
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
	return nil
}

func constrainSecretSchema(schema *jsonschema.Schema, model string) {
	if property := schema.Properties["agent_type"]; property != nil {
		property.Pattern = secretScopePattern
		maximum := 63
		property.MaxLength = &maximum
	}
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
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": OpenAPIRef("Secret")}},
	})
	list["description"] = "Returns metadata for one agent type, sorted by name, including expired secrets. Reads project status without changing stored state. No values are returned. Unknown or repeated query parameters and malformed query encoding are rejected."
	list["parameters"] = []any{
		secretParameter("agent_type", "query", true, map[string]any{"type": "string", "pattern": secretScopePattern, "maxLength": 63}),
	}
	create := secretOperation("createSecret", "Create a secret", OpenAPIRef("Secret"))
	create["description"] = "Encrypts a new named secret for an agent type, independent of operations. The server assigns the ID and version 1. Existing keys return secret_conflict. Returns metadata only. Query parameters are not accepted."
	setSecretBody(create, "CreateSecretRequest")
	responses := create["responses"].(map[string]any)
	delete(responses, "200")
	success := OpenAPIResponse("Created metadata", OpenAPIRef("Secret"))
	success["headers"] = map[string]any{"Location": map[string]any{
		"description": "Relative URL of the created secret metadata.", "required": true,
		"schema": map[string]any{"type": "string", "format": "uri-reference"},
	}}
	responses["201"] = success
	paths[secretsPath] = map[string]any{"get": list, "post": create}

	get := secretOperation("getSecret", "Get secret metadata", OpenAPIRef("Secret"))
	get["description"] = "Returns metadata and current availability for the exact scoped key. No values are returned. Query parameters are not accepted."
	get["responses"].(map[string]any)["404"] = OpenAPIResponse("secret_not_found", OpenAPIRef("Error"))
	replace := secretOperation("replaceSecret", "Replace a secret", OpenAPIRef("Secret"))
	replace["description"] = "Replaces the value and expiry only when the saved ID and version match. Preserves identity and creation time, and increments version. The body must contain a new value. Omitting expires_at removes any previous expiry. A deleted and recreated key has a new ID, so a stale replacement cannot change it. Returns metadata only. Query parameters are not accepted."
	setSecretBody(replace, "ReplaceSecretRequest")
	replace["responses"].(map[string]any)["404"] = OpenAPIResponse("secret_not_found", OpenAPIRef("Error"))
	remove := secretOperation("deleteSecret", "Delete a secret", nil)
	remove["description"] = "Deletes only the specified saved ID and version. A stale ID or version conflicts with a replacement or recreated key. Deletion of an absent key retires the supplied ID and succeeds, preventing delayed creation with that identity. Unknown or repeated query parameters and malformed query encoding are rejected."
	responses = remove["responses"].(map[string]any)
	delete(responses, "200")
	responses["204"] = map[string]any{"description": "Secret deleted or supplied identity already absent."}
	responses["409"] = OpenAPIResponse("secret_conflict", OpenAPIRef("Error"))
	for _, route := range []map[string]any{get, replace, remove} {
		route["parameters"] = []any{
			secretParameter("agent_type", "path", true, map[string]any{"type": "string", "pattern": secretScopePattern, "maxLength": 63}),
			secretParameter("name", "path", true, map[string]any{"type": "string", "pattern": secretNamePattern}),
		}
	}
	remove["parameters"] = append(remove["parameters"].([]any),
		secretParameter("id", "query", true, map[string]any{"type": "string", "format": "uuid", "pattern": secretIDPattern}),
		secretParameter("version", "query", true, map[string]any{"type": "integer", "minimum": 1, "maximum": ^uint64(0)}),
	)
	paths[secretPath] = map[string]any{"get": get, "put": replace, "delete": remove}
}

func secretOperation(id, summary string, schema any) map[string]any {
	operation := OpenAPIQueryOperation(id, summary, "Secrets", schema)
	responses := operation["responses"].(map[string]any)
	responses["400"] = OpenAPIResponse("invalid_request or invalid_request_origin", OpenAPIRef("Error"))
	responses["503"] = OpenAPIResponse("secrets_unavailable, secret_write_uncertain or request_canceled. Secret management requires configured encryption. An uncertain write takes precedence over a simultaneous timeout; read metadata to reconcile before retrying.", OpenAPIRef("Error"))
	responses["504"] = OpenAPIResponse("request_timeout after at most 10 seconds. A write may have succeeded; read metadata before retrying. Requests are never retried automatically.", OpenAPIRef("Error"))
	return operation
}

func setSecretBody(operation map[string]any, schema string) {
	operation["requestBody"] = map[string]any{
		"required":    true,
		"description": "At most 1 MiB. Requires application/json; charset parameters are accepted. Unknown fields, invalid JSON or invalid values return invalid_request. Secret values are write-only.",
		"content":     map[string]any{"application/json": map[string]any{"schema": OpenAPIRef(schema)}},
	}
	responses := operation["responses"].(map[string]any)
	responses["409"] = OpenAPIResponse("secret_conflict", OpenAPIRef("Error"))
	responses["413"] = OpenAPIResponse("request_too_large", OpenAPIRef("Error"))
	responses["415"] = OpenAPIResponse("unsupported_media_type", OpenAPIRef("Error"))
}

func secretParameter(name, location string, required bool, schema any) map[string]any {
	return map[string]any{"name": name, "in": location, "required": required, "schema": schema}
}
