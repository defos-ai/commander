package agents

import (
	"fmt"
	"reflect"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/google/jsonschema-go/jsonschema"
)

func RegisterOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
	dependencyType := reflect.TypeFor[models.AgentDependencyResponse]()
	dependency, err := jsonschema.ForType(dependencyType, options)
	if err != nil {
		return fmt.Errorf("describe API AgentDependency: %w", err)
	}
	dependency.Properties["name"].Description = "Software name; matches executable missing_requirements by exact name."
	dependency.Properties["description"].Description = "Installation guidance for the agent's execution host."
	dependency.Properties["version"].Description = "Optional supported version or range, declared by the agent. This is not an installed-version observation."
	components["AgentDependency"] = dependency
	options.TypeSchemas[dependencyType] = dependency
	typeOf := reflect.TypeFor[models.AgentResponse]()
	schema, err := jsonschema.ForType(typeOf, options)
	if err != nil {
		return fmt.Errorf("describe API Agent: %w", err)
	}
	schema.Properties["version"].Description = "Agent package/build version reported by the agent; absent when unreported. Distinct from framework and dependency versions; does not establish readiness."
	schema.Properties["status"].Description = "Agent state. stop_requested is durable controller intent, not confirmed shutdown. A stale lease is unavailable unless stop_requested or stopped."
	schema.Properties["stop_command_id"].Description = "Read-only graceful stop command identity, retained across retries and confirmation; absent before a stop is requested."
	schema.Properties["stop_requested_at"].Description = "Read-only time when the controller first accepted graceful stop; absent before a stop is requested."
	schema.Properties["stale"].Description = "Whether the heartbeat lease has expired at query time."
	schema.Properties["work"].Description = "Current execution identifiers and action name; absent when no work is reported."
	schema.Properties["dependencies"].Description = "Optional external-software installation guidance. Match missing requirements of kind executable by exact name. Declarations do not establish readiness or enforce versions."
	components["Agent"] = schema
	options.TypeSchemas[typeOf] = schema
	if err := registerStopSchemas(components, options); err != nil {
		return err
	}
	registerAgentPaths(paths)
	return nil
}

func registerStopSchemas(components map[string]any, options *jsonschema.ForOptions) error {
	for _, entry := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"StopSelectorRequest", reflect.TypeFor[models.StopSelectorRequest]()},
		{"StopAgentsRequest", reflect.TypeFor[models.StopAgentsRequest]()},
		{"AgentStopError", reflect.TypeFor[models.AgentStopErrorResponse]()},
		{"StopAgentsResponse", reflect.TypeFor[models.StopAgentsResponse]()},
	} {
		schema, err := jsonschema.ForType(entry.typeOf, options)
		if err != nil {
			return fmt.Errorf("describe API %s: %w", entry.name, err)
		}
		schema.AdditionalProperties = &jsonschema.Schema{Not: &jsonschema.Schema{}}
		switch entry.name {
		case "StopSelectorRequest":
			minimum := 1
			schema.MinProperties = &minimum
			for name, property := range schema.Properties {
				property.MinLength = &minimum
				property.Description = "Exact agent " + name + " filter; supplied filters intersect."
			}
			schema.Properties["version"].Description = "Exact reported agent version; values such as stable/latest are compared literally without release alias resolution."
			schema.Properties["hostname"].Description = "Exact advertised server.hostname, without DNS resolution or case folding. For containers and Kubernetes this is the agent's container/Pod hostname, not its parent spawner or node."
		case "AgentStopError":
			schema.Properties["error"].Enum = []any{"agent_not_found", "agent_stop_uncertain", "agent_stop_not_requested"}
		case "StopAgentsResponse":
			items := schema.Properties["items"]
			items.Type, items.Types, items.AnyOf = "array", nil, nil
		}
		components[entry.name] = schema
		options.TypeSchemas[entry.typeOf] = schema
	}
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
	stop := endpoints.OpenAPIQueryOperation("stopAgents", "Request graceful agent stops", "Agent", endpoints.OpenAPIRef("StopAgentsResponse"))
	stop["description"] = "The controller captures agents matching the exact selector filters and requests graceful stop. At least one filter is required; id, type, version and hostname intersect. New assignments are fenced while accepted work drains. 202 means at least one item is stop_requested, not confirmed shutdown. 200 also covers no matches or per-agent failures: always inspect errors. Read each agent by ID for confirmation or after uncertainty; do not retry automatically. Does not change host restart policies."
	stop["requestBody"] = map[string]any{
		"required": true,
		"content": map[string]any{"application/json": map[string]any{
			"schema": endpoints.OpenAPIRef("StopAgentsRequest"),
		}},
	}
	responses := stop["responses"].(map[string]any)
	responses["200"] = endpoints.OpenAPIResponse("No pending stops in returned items; inspect per-agent errors, including when no items are returned", endpoints.OpenAPIRef("StopAgentsResponse"))
	responses["202"] = endpoints.OpenAPIResponse("At least one graceful stop requested; inspect every item and error", endpoints.OpenAPIRef("StopAgentsResponse"))
	responses["400"] = endpoints.OpenAPIResponse("invalid_query, invalid_request or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	responses["413"] = endpoints.OpenAPIResponse("request_too_large", endpoints.OpenAPIRef("Error"))
	responses["415"] = endpoints.OpenAPIResponse("unsupported_media_type", endpoints.OpenAPIRef("Error"))
	responses["503"] = endpoints.OpenAPIResponse("agent_controls_unavailable or agent_stop_uncertain; read saved agent state before retrying", endpoints.OpenAPIRef("Error"))
	delete(responses, "504")
	paths["/api/v1/agents/stop"] = map[string]any{"post": stop}
}
