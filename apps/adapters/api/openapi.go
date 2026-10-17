package api

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	operationendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/operations"
	"github.com/defos-ai/commander/apps/adapters/api/models"

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
	}}
	for _, model := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"About", reflect.TypeFor[models.AboutResponse]()},
		{"MissionRuleParams", reflect.TypeFor[models.MissionRuleParamsResponse]()},
		{"MissionRule", reflect.TypeFor[models.MissionRuleResponse]()},
		{"Mission", reflect.TypeFor[models.MissionResponse]()},
		{"ActionRequirement", reflect.TypeFor[models.ActionRequirementResponse]()},
		{"Action", reflect.TypeFor[models.ActionResponse]()},
		{"Agent", reflect.TypeFor[models.AgentResponse]()},
		{"Controller", reflect.TypeFor[models.ControllerResponse]()},
		{"Report", reflect.TypeFor[models.ReportResponse]()},
		{"Artifact", reflect.TypeFor[models.ArtifactResponse]()},
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
		switch model.name {
		case "About":
			schema.Properties["runtime"].Enum = []any{"native", "docker", "docker-compose"}
			schema.Properties["application"].Enum = []any{"commander-api", "commander"}
		case "MissionRuleParams":
			schema.Properties["decision_by"].Enum = []any{"model"}
		case "MissionRule":
			schema.Properties["for_actions"].Description = "Actions whose executions this rule applies to."
		case "Mission":
			schema.Properties["rules"].Description = "Mission policies keyed by aggregate-results or decide-next-actions. Valid proposed actions run automatically by default. aggregate-results collects direct children of one execution; decide-next-actions with decision_by=model requests model review. Failures and empty proposals also require model review."
		case "ActionRequirement":
			schema.Properties["kind"].Enum = []any{"executable", "configuration", "secret"}
			schema.Properties["kind"].Description = "executable and configuration requirements need host setup. secret identifies an unavailable declared Commander-managed secret, scoped to the agent type."
			schema.Properties["name"].Description = "Requirement identifier, never its value. Managed secret names match [A-Za-z0-9_][A-Za-z0-9_.-]{0,127}."
			schema.Properties["sensitive"].Description = "Marks sensitive host configuration only; it does not identify a Commander-managed secret. Managed secrets use kind=secret without this flag."
		case "Action":
			schema.Properties["status"].Description = "Discovered readiness, normally unknown, ready or unavailable. Check the selected agent's action state before dispatch."
		case "Agent":
			schema.Properties["status"].Description = "Observed agent state. A stale lease is reported as unavailable unless the agent has stopped."
			schema.Properties["stale"].Description = "Whether the heartbeat lease has expired at query time."
			schema.Properties["work"].Description = "Current execution identifiers and action name; absent when no work is reported."
		case "Controller":
			schema.Properties["agent_selector"].Description = "Policy used to select an agent for each task."
			schema.Properties["rule_selector"].Description = "Policy used to select mission rules when starting an operation."
			schema.Properties["status"].Enum = []any{"starting", "ready", "degraded", "draining", "stopped", "unavailable"}
			schema.Properties["status"].Description = "Observed controller state. A stale lease is reported as unavailable unless the controller has stopped."
			schema.Properties["status_reason"].Description = "Reason for the observed state, when one is available."
			schema.Properties["stale"].Description = "Whether a non-stopped controller's heartbeat lease has expired at query time."
		case "Report":
			schema.Properties["output"].Description = "Saved mission output; any JSON value, including null, arrays and scalars."
		}
		components[model.name] = schema
		options.TypeSchemas[model.typeOf] = schema
	}
	paths := map[string]any{
		"/aboutz": map[string]any{"get": map[string]any{
			"operationId": "getAbout", "summary": "Deployment information", "tags": []string{"Server"}, "security": []any{},
			"description": "Public deployment metadata. Host, origin and trusted-proxy rules still apply. HEAD returns the same headers without a body.",
			"responses": map[string]any{
				"200": endpoints.OpenAPIResponse("Deployment runtime and application", endpoints.OpenAPIRef("About")),
				"400": endpoints.OpenAPIResponse("invalid_request_origin", endpoints.OpenAPIRef("Error")),
				"403": endpoints.OpenAPIResponse("host_not_allowed or cross_origin_request", endpoints.OpenAPIRef("Error")),
			},
		}},
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
		{"controllers", "Controller", "controller_id", "unknown_controller"},
	} {
		listSchema := map[string]any{
			"type": "object", "required": []string{"items"},
			"properties": map[string]any{"items": map[string]any{"type": "array", "items": endpoints.OpenAPIRef(catalog.model)}},
		}
		list := endpoints.OpenAPIQueryOperation("list"+catalog.model+"s", "List "+catalog.path, catalog.model, listSchema)
		list["description"] = "Returns all records, sorted by name (missions/actions) or ID (agents/controllers). Agent and controller freshness reflect the current lease."
		paths["/api/v1/"+catalog.path] = map[string]any{"get": list}
		detail := endpoints.OpenAPIQueryOperation("get"+catalog.model, "Get "+catalog.model, catalog.model, endpoints.OpenAPIRef(catalog.model))
		detail["parameters"] = []any{map[string]any{
			"name": catalog.parameter, "in": "path", "required": true, "schema": map[string]any{"type": "string", "minLength": 1},
		}}
		detail["responses"].(map[string]any)["404"] = endpoints.OpenAPIResponse(catalog.missingCode, endpoints.OpenAPIRef("Error"))
		paths["/api/v1/"+catalog.path+"/{"+catalog.parameter+"}"] = map[string]any{"get": detail}
	}
	report := endpoints.OpenAPIQueryOperation("getReport", "Get a report", "Reports", endpoints.OpenAPIRef("Report"))
	report["description"] = "Returns the full immutable saved report directly from the report store. Reads do not execute work or change saved state."
	report["parameters"] = []any{openAPIUUIDParameter("report_id")}
	report["responses"].(map[string]any)["400"] = endpoints.OpenAPIResponse("invalid_report_id or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	report["responses"].(map[string]any)["404"] = endpoints.OpenAPIResponse("report_not_found", endpoints.OpenAPIRef("Error"))
	paths["/api/v1/reports/{report_id}"] = map[string]any{"get": report}
	openAPIArtifactPaths(paths)
	if err := operationendpoints.RegisterOpenAPI(paths, components, options); err != nil {
		return nil, err
	}
	if err := endpoints.RegisterSecretsOpenAPI(paths, components, options); err != nil {
		return nil, err
	}
	if err := endpoints.RegisterSecretRequestsOpenAPI(paths, components, options); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title": "Commander API", "version": "0.1.0",
			"description": "Read Commander catalogs, saved operation state, reports and artifacts, manage encrypted secrets when enabled, or send operation commands to the controller over the bus. Health probes and deployment metadata are public; all resource requests require Basic authentication. Reads do not execute work or mutate saved records.",
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

func openAPIUUIDParameter(name string) map[string]any {
	return map[string]any{
		"name": name, "in": "path", "required": true, "description": "Canonical lowercase UUID.",
		"schema": map[string]any{"type": "string", "format": "uuid", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"},
	}
}

func openAPIArtifactPaths(paths map[string]any) {
	artifact := endpoints.OpenAPIQueryOperation("getArtifact", "Get artifact metadata", "Artifacts", endpoints.OpenAPIRef("Artifact"))
	artifact["description"] = "Returns saved artifact metadata and file descriptors directly from the database. Reads do not contact the controller or change saved state."
	artifact["parameters"] = []any{openAPIUUIDParameter("artifact_id")}
	artifact["responses"].(map[string]any)["400"] = endpoints.OpenAPIResponse("invalid_artifact_id or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	artifact["responses"].(map[string]any)["404"] = endpoints.OpenAPIResponse("artifact_not_found", endpoints.OpenAPIRef("Error"))
	paths["/api/v1/artifacts/{artifact_id}"] = map[string]any{"get": artifact}

	file := endpoints.OpenAPIQueryOperation("getArtifactFile", "Download an artifact file", "Artifacts", nil)
	file["description"] = "Returns raw file bytes as an attachment after checking artifact membership and verifying the saved size and SHA-256. Content-Type uses the stored media type, falling back to application/octet-stream when invalid. HEAD returns the same headers without a body. Downloads are read fully before responding; byte ranges are not supported."
	file["parameters"] = []any{openAPIUUIDParameter("artifact_id"), openAPIUUIDParameter("file_id")}
	responses := file["responses"].(map[string]any)
	responses["200"] = map[string]any{
		"description": "Verified file bytes, including empty files",
		"content":     map[string]any{"*/*": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}},
		"headers": map[string]any{
			"Content-Disposition": map[string]any{"description": "Attachment with a sanitized filename, encoded for Unicode when needed.", "schema": map[string]any{"type": "string"}},
			"Content-Length":      map[string]any{"description": "Verified file length in bytes.", "schema": map[string]any{"type": "integer", "minimum": 0}},
		},
	}
	responses["400"] = endpoints.OpenAPIResponse("invalid_artifact_id, invalid_file_id or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	responses["404"] = endpoints.OpenAPIResponse("artifact_not_found or artifact_file_not_found (unknown file, file not belonging to this artifact, or missing bytes)", endpoints.OpenAPIRef("Error"))
	paths["/api/v1/artifacts/{artifact_id}/files/{file_id}"] = map[string]any{"get": file}
}
