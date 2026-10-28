package agents

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/google/jsonschema-go/jsonschema"
)

func RegisterOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
	for _, model := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"Agent", reflect.TypeFor[models.AgentResponse]()},
		{"CatalogAgent", reflect.TypeFor[models.CatalogAgentResponse]()},
		{"CreateCatalogAgentRequest", reflect.TypeFor[models.CreateCatalogAgentRequest]()},
		{"SetCatalogAgentTrustRequest", reflect.TypeFor[models.SetCatalogAgentTrustRequest]()},
	} {
		schema, err := jsonschema.ForType(model.typeOf, options)
		if err != nil {
			return fmt.Errorf("describe API %s: %w", model.name, err)
		}
		constrainCatalogSchema(schema, model.name)
		data, err := json.Marshal(schema)
		if err != nil {
			return fmt.Errorf("encode API %s schema: %w", model.name, err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("decode API %s schema: %w", model.name, err)
		}
		// JSON Schema's Go type stores numeric bounds as float64.
		if version, ok := document["properties"].(map[string]any)["version"].(map[string]any); ok {
			version["maximum"] = ^uint64(0)
		}
		components[model.name] = document
		options.TypeSchemas[model.typeOf] = schema
	}
	registerAgentPaths(paths)
	registerCatalogPaths(paths)
	return nil
}

func constrainCatalogSchema(schema *jsonschema.Schema, model string) {
	if model == "Agent" {
		schema.Properties["status"].Description = "Observed agent state. A stale lease is reported as unavailable unless the agent has stopped."
		schema.Properties["stale"].Description = "Whether the heartbeat lease has expired at query time."
		schema.Properties["work"].Description = "Current execution identifiers and action name; absent when no work is reported."
		return
	}
	if property := schema.Properties["name"]; property != nil {
		property.Pattern = `^[a-z][a-z0-9-]*$`
		property.Description = "Display alias; package is the immutable catalog key. Names need not be unique."
	}
	if property := schema.Properties["description"]; property != nil {
		property.MinLength = new(1)
		property.Description = "Nonempty description without leading or trailing whitespace."
	}
	if property := schema.Properties["package"]; property != nil {
		property.Description = "Canonical full selector github.com/owner/repository[/module/path]@vMAJOR.MINOR.PATCH or @stable, with no URL prefix, omitted version, query or fragment. Different release selectors are separate entries."
	}
	if property := schema.Properties["visibility"]; property != nil {
		property.Enum = []any{"public", "private"}
		property.Description = "Repository access metadata; independent of trust."
	}
	if property := schema.Properties["version"]; property != nil {
		property.Minimum = new(float64(1))
		property.Description = "Saved business version; an explicit trust decision requires the current version and increments it by one."
	}
	if model == "CatalogAgent" {
		schema.Description = "Saved package metadata, separate from live agents and action availability. Catalog trust is recorded for this exact selector; it does not yet authorize runtime secret delivery."
		for _, property := range schema.Properties {
			property.ReadOnly = true
		}
		schema.Required = slices.DeleteFunc(schema.Required, func(field string) bool { return field == "trust_updated_by" || field == "trust_updated_at" })
		schema.Properties["trust_updated_by"].Description = "Authenticated API username responsible for the latest explicit trust decision. Present together with trust_updated_at after a decision."
		schema.Properties["trust_updated_at"].Description = "Server timestamp of the latest explicit trust decision. Present together with trust_updated_by after a decision."
		return
	}
	schema.AdditionalProperties = &jsonschema.Schema{Not: &jsonschema.Schema{}}
	if model == "SetCatalogAgentTrustRequest" {
		trusted := schema.Properties["trusted"]
		trusted.Type, trusted.Types, trusted.AnyOf = "boolean", nil, nil
		trusted.Description = "Required boolean. Grants require an exact release; @stable may only be revoked. Omission and null are invalid."
	}
}

func registerCatalogPaths(paths map[string]any) {
	list := catalogOperation("listCatalogAgents", "List saved agent packages", map[string]any{
		"type": "object", "required": []string{"items"},
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": endpoints.OpenAPIRef("CatalogAgent")}},
	})
	list["description"] = "Returns saved entries sorted by canonical package selector. Empty catalogs return a nonnull items array. No query parameters are accepted. Reads need neither the controller nor external catalog access."
	create := catalogOperation("createCatalogAgent", "Add an untrusted agent package", endpoints.OpenAPIRef("CatalogAgent"))
	create["description"] = "Creates one immutable canonical package identity as untrusted, version 1, with server timestamps. Trust, audit fields and version cannot be supplied. Existing packages return catalog_entry_exists even for identical metadata. No query parameters are accepted."
	setCatalogBody(create, "CreateCatalogAgentRequest")
	responses := create["responses"].(map[string]any)
	delete(responses, "200")
	success := endpoints.OpenAPIResponse("Created untrusted package", endpoints.OpenAPIRef("CatalogAgent"))
	success["headers"] = map[string]any{"Location": map[string]any{
		"description": "Relative detail URL with the full canonical selector escaped as one path segment.", "required": true,
		"schema": map[string]any{"type": "string", "format": "uri-reference"},
	}}
	responses["201"] = success
	responses["409"] = endpoints.OpenAPIResponse("catalog_entry_exists", endpoints.OpenAPIRef("Error"))
	paths[catalogAgentsPath] = map[string]any{"get": list, "post": create}

	get := catalogOperation("getCatalogAgent", "Get a saved agent package", endpoints.OpenAPIRef("CatalogAgent"))
	get["description"] = "Reads one canonical package identity. Escape the entire package selector as one URL path segment, including its slashes. No query parameters are accepted."
	trust := catalogOperation("setCatalogAgentTrust", "Grant or revoke package trust", endpoints.OpenAPIRef("CatalogAgent"))
	trust["description"] = "Records an explicit decision only when version matches the saved entry. Grants require an exact release selector; mutable @stable entries may only be revoked. Updates increment the version, preserve identity and creation time, and record the authenticated API username and server time. The request cannot choose its actor or audit timestamp. No automatic retries; after an uncertain response read the package before deciding whether to submit another change. This records catalog policy; runtime secret enforcement is separate. No query parameters are accepted."
	setCatalogBody(trust, "SetCatalogAgentTrustRequest")
	trust["responses"].(map[string]any)["409"] = endpoints.OpenAPIResponse("catalog_conflict", endpoints.OpenAPIRef("Error"))
	for _, operation := range []map[string]any{get, trust} {
		operation["parameters"] = []any{map[string]any{
			"name": "package", "in": "path", "required": true,
			"description": "Full canonical selector, encoded as one URL path segment.",
			"schema":      map[string]any{"type": "string"},
		}}
		operation["responses"].(map[string]any)["404"] = endpoints.OpenAPIResponse("catalog_entry_not_found", endpoints.OpenAPIRef("Error"))
	}
	paths[catalogAgentPath] = map[string]any{"get": get, "patch": trust}
}

func registerAgentPaths(paths map[string]any) {
	list := endpoints.OpenAPIQueryOperation("listAgents", "List agents", "Agent", map[string]any{
		"type": "object", "required": []string{"items"},
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": endpoints.OpenAPIRef("Agent")}},
	})
	list["description"] = "Returns all registered agents, sorted by ID. Agent freshness reflects the current lease."
	paths["/api/v1/agents"] = map[string]any{"get": list}
	detail := endpoints.OpenAPIQueryOperation("getAgent", "Get Agent", "Agent", endpoints.OpenAPIRef("Agent"))
	detail["parameters"] = []any{map[string]any{
		"name": "agent_id", "in": "path", "required": true, "schema": map[string]any{"type": "string", "minLength": 1},
	}}
	detail["responses"].(map[string]any)["404"] = endpoints.OpenAPIResponse("agent_not_found", endpoints.OpenAPIRef("Error"))
	paths["/api/v1/agents/{agent_id}"] = map[string]any{"get": detail}
}

func catalogOperation(id, summary string, schema any) map[string]any {
	operation := endpoints.OpenAPIQueryOperation(id, summary, "Agent catalog", schema)
	responses := operation["responses"].(map[string]any)
	responses["400"] = endpoints.OpenAPIResponse("invalid_request or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	responses["503"] = endpoints.OpenAPIResponse("catalog_write_uncertain or request_canceled. Uncertain writes take precedence over simultaneous cancellation or timeout; read saved state before another mutation.", endpoints.OpenAPIRef("Error"))
	responses["504"] = endpoints.OpenAPIResponse("request_timeout after at most 10 seconds. Writes may have succeeded; read saved state before another mutation.", endpoints.OpenAPIRef("Error"))
	return operation
}

func setCatalogBody(operation map[string]any, schema string) {
	operation["requestBody"] = map[string]any{
		"required":    true,
		"description": "At most 1 MiB. Requires application/json; charset parameters are accepted. Unknown fields and invalid values are rejected.",
		"content":     map[string]any{"application/json": map[string]any{"schema": endpoints.OpenAPIRef(schema)}},
	}
	responses := operation["responses"].(map[string]any)
	responses["413"] = endpoints.OpenAPIResponse("request_too_large", endpoints.OpenAPIRef("Error"))
	responses["415"] = endpoints.OpenAPIResponse("unsupported_media_type", endpoints.OpenAPIRef("Error"))
}
