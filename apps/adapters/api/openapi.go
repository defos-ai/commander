package api

import (
	"encoding/json"
	"fmt"
	"reflect"

	"commander/adapters/api/endpoints"
	operationendpoints "commander/adapters/api/endpoints/operations"
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/missions"

	"github.com/google/jsonschema-go/jsonschema"
)

func openAPIDocument() ([]byte, error) {
	components := map[string]any{
		"Error": map[string]any{
			"type": "object", "required": []string{"error"},
			"properties": map[string]any{"error": map[string]any{"type": "string"}},
		},
		"Health": map[string]any{
			"type": "object", "required": []string{"status"},
			"properties": map[string]any{"status": map[string]any{"type": "string", "enum": []string{"ok", "unavailable"}}},
		},
	}
	options := &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[json.RawMessage](): {},
		reflect.TypeFor[actions.Status](): {
			Type: "string", Description: "Discovered readiness, normally unknown, ready or unavailable. Check the selected agent's action state before dispatch.",
		},
		reflect.TypeFor[actions.RequirementKind](): {
			Type: "string", Enum: []any{actions.ExecutableRequirement, actions.ConfigurationRequirement, actions.PermissionRequirement},
		},
		reflect.TypeFor[agents.AgentStatus](): {
			Type: "string", Description: "Observed agent state. A stale lease is reported as unavailable unless the agent has stopped.",
		},
		reflect.TypeFor[missions.ContinueWhen](): {Type: "string", Enum: []any{missions.WhenEach, missions.WhenAll}},
		reflect.TypeFor[missions.Decision]():     {Type: "string", Enum: []any{missions.Model}},
	}}
	for _, model := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"Mission", reflect.TypeFor[missions.Mission]()},
		{"Action", reflect.TypeFor[actions.Action]()},
		{"Agent", reflect.TypeFor[agents.Agent]()},
	} {
		schema, err := jsonschema.ForType(model.typeOf, options)
		if err != nil {
			return nil, fmt.Errorf("describe API %s: %w", model.name, err)
		}
		for _, name := range []string{"input_schema", "output_schema"} {
			if property := schema.Properties[name]; property != nil {
				property.Type = "object"
				property.Description = "JSON Schema for the accepted input or produced output. An empty object allows any JSON value."
			}
		}
		if model.name == "Agent" {
			schema.Properties["stale"].Description = "Whether the heartbeat lease has expired at query time."
			schema.Properties["work"].Description = "Current execution identifiers and action name; absent when no work is reported."
		}
		components[model.name] = schema
		options.TypeSchemas[model.typeOf] = schema
	}
	paths := map[string]any{
		"/healthz": map[string]any{"get": map[string]any{
			"operationId": "getHealth", "summary": "Process health", "tags": []string{"Health"}, "security": []any{},
			"responses": map[string]any{"200": endpoints.OpenAPIResponse("Process is running", endpoints.OpenAPIRef("Health"))},
		}},
		"/readyz": map[string]any{"get": map[string]any{
			"operationId": "getReadiness", "summary": "Metadata database readiness", "tags": []string{"Health"}, "security": []any{},
			"description": "Checks metadata database readability. Does not check the controller, broker or artifact file storage.",
			"responses": map[string]any{
				"200": endpoints.OpenAPIResponse("Database is readable", endpoints.OpenAPIRef("Health")),
				"503": endpoints.OpenAPIResponse("Database is unavailable", endpoints.OpenAPIRef("Health")),
			},
		}},
	}
	for _, catalog := range []struct {
		path, model, parameter, missingCode string
	}{
		{"missions", "Mission", "mission_name", "mission_not_found"},
		{"actions", "Action", "action_name", "action_not_found"},
		{"agents", "Agent", "agent_id", "agent_not_found"},
	} {
		listSchema := map[string]any{
			"type": "object", "required": []string{"items"},
			"properties": map[string]any{"items": map[string]any{"type": "array", "items": endpoints.OpenAPIRef(catalog.model)}},
		}
		list := endpoints.OpenAPIQueryOperation("list"+catalog.model+"s", "List "+catalog.path, catalog.model, listSchema)
		list["description"] = "Returns all records, sorted by name (missions/actions) or ID (agents). Agent freshness reflects the current lease."
		paths["/api/v1/"+catalog.path] = map[string]any{"get": list}
		detail := endpoints.OpenAPIQueryOperation("get"+catalog.model, "Get "+catalog.model, catalog.model, endpoints.OpenAPIRef(catalog.model))
		detail["parameters"] = []any{map[string]any{
			"name": catalog.parameter, "in": "path", "required": true, "schema": map[string]any{"type": "string", "minLength": 1},
		}}
		detail["responses"].(map[string]any)["404"] = endpoints.OpenAPIResponse(catalog.missingCode, endpoints.OpenAPIRef("Error"))
		paths["/api/v1/"+catalog.path+"/{"+catalog.parameter+"}"] = map[string]any{"get": detail}
	}
	if err := operationendpoints.RegisterOpenAPI(paths, components, options); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title": "Commander API", "version": "0.1.0",
			"description": "Read Commander catalogs and saved operation state, or send operation commands to the controller over the bus. Health probes are public; all resource requests require Basic authentication. Reads do not execute work or mutate saved records.",
		},
		"servers":  []any{map[string]any{"url": "/"}},
		"security": []any{map[string]any{"basicAuth": []string{}}},
		"paths":    paths,
		"components": map[string]any{
			"schemas": components,
			"securitySchemes": map[string]any{"basicAuth": map[string]any{
				"type": "http", "scheme": "basic", "description": "API_USERNAME and API_PASSWORD configured on the server. Use HTTPS outside localhost.",
			}},
		},
	})
}
