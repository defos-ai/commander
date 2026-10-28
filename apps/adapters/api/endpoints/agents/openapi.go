package agents

import (
	"fmt"
	"reflect"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/google/jsonschema-go/jsonschema"
)

func RegisterOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
	typeOf := reflect.TypeFor[models.AgentResponse]()
	schema, err := jsonschema.ForType(typeOf, options)
	if err != nil {
		return fmt.Errorf("describe API Agent: %w", err)
	}
	schema.Properties["status"].Description = "Observed agent state. A stale lease is reported as unavailable unless the agent has stopped."
	schema.Properties["stale"].Description = "Whether the heartbeat lease has expired at query time."
	schema.Properties["work"].Description = "Current execution identifiers and action name; absent when no work is reported."
	components["Agent"] = schema
	options.TypeSchemas[typeOf] = schema
	registerAgentPaths(paths)
	return nil
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
