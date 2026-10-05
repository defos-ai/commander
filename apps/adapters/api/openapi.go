package api

import (
	"encoding/json"
	"fmt"
	"reflect"

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
			"responses": map[string]any{"200": openAPIResponse("Process is running", openAPIRef("Health"))},
		}},
		"/readyz": map[string]any{"get": map[string]any{
			"operationId": "getReadiness", "summary": "Metadata database readiness", "tags": []string{"Health"}, "security": []any{},
			"description": "Checks metadata database readability. Does not check the controller, broker or artifact file storage.",
			"responses": map[string]any{
				"200": openAPIResponse("Database is readable", openAPIRef("Health")),
				"503": openAPIResponse("Database is unavailable", openAPIRef("Health")),
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
			"properties": map[string]any{"items": map[string]any{"type": "array", "items": openAPIRef(catalog.model)}},
		}
		list := openAPICatalogOperation("list"+catalog.model+"s", "List "+catalog.path, catalog.model, listSchema)
		list["description"] = "Returns all records, sorted by name (missions/actions) or ID (agents). Agent freshness reflects the current lease."
		paths["/api/v1/"+catalog.path] = map[string]any{"get": list}
		detail := openAPICatalogOperation("get"+catalog.model, "Get "+catalog.model, catalog.model, openAPIRef(catalog.model))
		detail["parameters"] = []any{map[string]any{
			"name": catalog.parameter, "in": "path", "required": true, "schema": map[string]any{"type": "string", "minLength": 1},
		}}
		detail["responses"].(map[string]any)["404"] = openAPIResponse(catalog.missingCode, openAPIRef("Error"))
		paths["/api/v1/"+catalog.path+"/{"+catalog.parameter+"}"] = map[string]any{"get": detail}
	}
	return json.Marshal(map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title": "Commander API", "version": "0.1.0",
			"description": "Query Commander catalogs. Health probes are public; catalog requests require Basic authentication.",
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

func openAPICatalogOperation(id, summary, tag string, schema any) map[string]any {
	return map[string]any{
		"operationId": id, "summary": summary, "tags": []string{tag},
		"responses": map[string]any{
			"200": openAPIResponse("Success", schema),
			"400": openAPIResponse("invalid_request_origin", openAPIRef("Error")),
			"401": openAPIResponse("authentication_required", openAPIRef("Error")),
			"403": openAPIResponse("host_not_allowed or cross_origin_request", openAPIRef("Error")),
			"500": openAPIResponse("internal_error", openAPIRef("Error")),
			"504": openAPIResponse("request_timeout", openAPIRef("Error")),
		},
	}
}

func openAPIResponse(description string, schema any) map[string]any {
	return map[string]any{
		"description": description,
		"content":     map[string]any{"application/json": map[string]any{"schema": schema}},
	}
}

func openAPIRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}
