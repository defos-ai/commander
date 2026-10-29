package api

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	agentendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/agents"
	operationendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/operations"
	secretendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/secrets"
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
		{"ActionDefinition", reflect.TypeFor[models.ActionDefinitionResponse]()},
		{"Mission", reflect.TypeFor[models.MissionResponse]()},
		{"MissionRuleParamsRequest", reflect.TypeFor[models.MissionRuleParamsRequest]()},
		{"MissionRuleRequest", reflect.TypeFor[models.MissionRuleRequest]()},
		{"ActionDefinitionRequest", reflect.TypeFor[models.ActionDefinitionRequest]()},
		{"CreateMissionRequest", reflect.TypeFor[models.CreateMissionRequest]()},
		{"ActionRequirement", reflect.TypeFor[models.ActionRequirementResponse]()},
		{"Action", reflect.TypeFor[models.ActionResponse]()},
		{"RuntimeSession", reflect.TypeFor[models.RuntimeSessionResponse]()},
		{"ControlSessionRequest", reflect.TypeFor[models.ControlSessionRequest]()},
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
		if objectives := schema.Properties["objectives"]; objectives != nil {
			objectives.Type, objectives.Types = "array", nil
			objectives.MinItems = new(1)
			objectives.Items.Pattern = `\S`
			objectives.Description = "Ordered mission objectives. Include at least one nonblank objective."
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
			schema.Properties["action_definitions"].Description = "Desired action contracts referenced by this mission. Metadata only: defining an action does not register an executor or satisfy readiness."
			schema.Properties["required_actions"].Description = "Actions included in mission preflight readiness checks."
			schema.Properties["optional_actions"].Description = "Additional actions the mission may use. Unscheduled optional actions do not block mission preflight; queued optional actions participate in readiness checks. Names are unique and must not overlap required_actions."
			schema.Properties["rules"].Description = "Mission policies keyed by aggregate-results, decide-next-actions or same-agent. Valid proposed actions run automatically by default. aggregate-results collects direct children of one execution; decide-next-actions with decision_by=model requests model review. same-agent binds its for_actions to the first selected agent instance within each operation, with no fallback; it accepts no parameters and does not order tasks. Failures and empty proposals also require model review."
		case "ActionRequirement":
			schema.Properties["kind"].Enum = []any{"executable", "configuration", "secret"}
			schema.Properties["kind"].Description = "executable and configuration requirements need host setup. secret identifies an unavailable declared Commander-managed secret shared by name across agents."
			schema.Properties["name"].Description = "Requirement identifier, never its value. Managed secret names match [A-Za-z0-9_][A-Za-z0-9_.-]{0,127}."
			schema.Properties["sensitive"].Description = "Marks sensitive host configuration only; it does not identify a Commander-managed secret. Managed secrets use kind=secret without this flag."
		case "Action":
			schema.Properties["session_mode"].Enum = []any{"", "create", "use"}
			schema.Properties["status"].Description = "Discovered readiness, normally unknown, ready or unavailable. Check the selected agent's action state before dispatch."
		case "RuntimeSession":
			schema.Properties["disposition"].Enum = []any{"reserved", "active", "closing", "released"}
			schema.Properties["release_reason"].Enum = []any{"closed", "abandoned", "cancelled_before_effect"}
			schema.Properties["owner_agent_id"].Description = "Exact process owner; empty while an undispatched creation is reserved."
		case "ControlSessionRequest":
			schema.Properties["method"].Enum = []any{"inspect", "verify", "close", "abandon"}
			for _, field := range []string{"id", "scope_id", "request_id"} {
				schema.Properties[field].Format = "uuid"
			}
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
		constrainMissionRequestSchema(schema, model.name)
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
	openAPISessionPaths(paths)
	openAPICreateMissionPath(paths)
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
	if err := secretendpoints.RegisterOpenAPI(paths, components, options); err != nil {
		return nil, err
	}
	if err := agentendpoints.RegisterOpenAPI(paths, components, options); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title": "Commander API", "version": "0.2.0",
			"description": "Create reusable missions, read Commander catalogs, saved operation state, reports and artifacts, manage encrypted secrets when enabled, or send operation commands to the controller over the bus. Health probes and deployment metadata are public; all resource requests require Basic authentication. Reads do not execute work or mutate saved records.",
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

func constrainMissionRequestSchema(schema *jsonschema.Schema, model string) {
	switch model {
	case "ActionDefinition", "ActionDefinitionRequest":
		schema.Properties["session_mode"].Enum = []any{"", "create", "use"}
		schema.Properties["name"].Pattern = `^\S(?:[\s\S]*\S)?$`
		if model == "ActionDefinitionRequest" {
			schema.Properties["description"].Type, schema.Properties["description"].Types = "", []string{"string", "null"}
		}
		for _, name := range []string{"input_schema", "output_schema"} {
			property := schema.Properties[name]
			property.Required = []string{"type"}
			property.Properties = map[string]*jsonschema.Schema{"type": {Const: new(any("object"))}}
			property.Description = "Locally resolvable JSON Schema explicitly declaring root type=object. External references are rejected."
		}
		secrets := schema.Properties["required_secrets"]
		secrets.Type, secrets.Types = "", []string{"array", "null"}
		secrets.UniqueItems = true
		secrets.Items.Pattern = `^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`
		secrets.Description = "Managed secret names, never secret values."
	case "MissionRuleParamsRequest":
		schema.Properties["decision_by"].Type, schema.Properties["decision_by"].Types = "", []string{"string", "null"}
		schema.Properties["decision_by"].Enum = []any{nil, "", "model"}
		schema.Properties["decision_by"].Description = "decide-next-actions requires model. Other rules reject a nonempty decision_by."
	case "MissionRuleRequest":
		forActions := schema.Properties["for_actions"]
		forActions.Type, forActions.Types = "array", nil
		forActions.MinItems = new(1)
		forActions.UniqueItems = true
		forActions.Items.Pattern = `^\S(?:[\s\S]*\S)?$`
		forActions.Description = "Nonempty, unique action names without surrounding whitespace."
	case "CreateMissionRequest":
		schema.Required = []string{"name", "objectives", "input_schema", "output_schema"}
		schema.Properties["name"].Pattern = `\S`
		schema.Properties["name"].Not = &jsonschema.Schema{Enum: []any{".", ".."}}
		schema.Properties["name"].Description = "Nonblank mission name; the dot segments '.' and '..' are invalid. Existing names return mission_exists and are never replaced."
		for _, name := range []string{"required_actions", "optional_actions", "starting_actions"} {
			property := schema.Properties[name]
			property.Type, property.Types = "", []string{"array", "null"}
			property.UniqueItems = true
			property.Items.Pattern = `^\S(?:[\s\S]*\S)?$`
		}
		schema.Properties["required_actions"].Description = "Unique action names without surrounding whitespace, including actions not currently installed. Checked during preflight."
		schema.Properties["optional_actions"].Description = "Additional unique action names; must not overlap required_actions. Unscheduled optional actions do not block preflight."
		schema.Properties["starting_actions"].Description = "Unique action names; each must appear in required_actions."
		schema.Properties["action_definitions"].Type, schema.Properties["action_definitions"].Types = "", []string{"array", "null"}
		schema.Properties["action_definitions"].MaxItems = new(100)
		schema.Properties["action_definitions"].Description = "Up to 100 desired action contracts, unique by name and referenced by required_actions or optional_actions. Metadata only: creation does not install actions, grant secrets or change readiness."
		rules := schema.Properties["rules"]
		rules.Type, rules.Types = "", []string{"object", "null"}
		rules.PropertyNames = &jsonschema.Schema{Enum: []any{"aggregate-results", "decide-next-actions", "same-agent"}}
		rules.Description = "Mission policies keyed by aggregate-results, decide-next-actions or same-agent. decide-next-actions requires params.decision_by=model. Shared mission validation checks cross-field constraints and resolves schema references locally; external schema references are rejected."
	}
}

func openAPICreateMissionPath(paths map[string]any) {
	create := endpoints.OpenAPIQueryOperation("createMission", "Create a mission", "Mission", endpoints.OpenAPIRef("Mission"))
	create["description"] = "Creates a reusable mission only if its exact name is absent. The shared mission validator checks action lists, desired action_definitions, rules and locally resolvable JSON Schema objects. Actions need not be installed. Definitions are metadata and do not register actions or satisfy readiness. No controller is required; creation does not run work or change existing operation snapshots. Query parameters are not accepted."
	create["requestBody"] = map[string]any{
		"required":    true,
		"description": "One JSON object, at most 1 MiB. Requires application/json; charset parameters are accepted. Unknown mission, action definition, rule and parameter fields are rejected. Schema objects may contain JSON Schema extension keywords. Invalid definitions return invalid_request.",
		"content":     map[string]any{"application/json": map[string]any{"schema": endpoints.OpenAPIRef("CreateMissionRequest")}},
	}
	responses := create["responses"].(map[string]any)
	delete(responses, "200")
	success := endpoints.OpenAPIResponse("Created mission definition", endpoints.OpenAPIRef("Mission"))
	success["headers"] = map[string]any{"Location": map[string]any{
		"description": "Relative URL of the created mission, with the name escaped as one path segment.", "required": true,
		"schema": map[string]any{"type": "string", "format": "uri-reference"},
	}}
	responses["201"] = success
	responses["400"] = endpoints.OpenAPIResponse("invalid_request or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	responses["409"] = endpoints.OpenAPIResponse("mission_exists. The existing definition is unchanged.", endpoints.OpenAPIRef("Error"))
	responses["413"] = endpoints.OpenAPIResponse("request_too_large", endpoints.OpenAPIRef("Error"))
	responses["415"] = endpoints.OpenAPIResponse("unsupported_media_type", endpoints.OpenAPIRef("Error"))
	responses["503"] = endpoints.OpenAPIResponse("request_canceled. Creation may have succeeded; read the mission by name before retrying.", endpoints.OpenAPIRef("Error"))
	responses["504"] = endpoints.OpenAPIResponse("request_timeout after at most 10 seconds. Creation may have succeeded; read the mission by name before retrying. Requests are never retried automatically.", endpoints.OpenAPIRef("Error"))
	paths["/api/v1/missions"].(map[string]any)["post"] = create
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

func openAPISessionPaths(paths map[string]any) {
	list := endpoints.OpenAPIQueryOperation("listRuntimeSessions", "List durable runtime sessions", "Sessions", map[string]any{"type": "object", "required": []string{"items"}, "properties": map[string]any{"items": map[string]any{"type": "array", "items": endpoints.OpenAPIRef("RuntimeSession")}}})
	list["description"] = "Lists current and retained sessions sorted by scope ID, then session ID. Optional scope and exact owner filters intersect. Omitted filters return all saved sessions. Unknown, repeated, empty or malformed query parameters are rejected. Reads do not change lifecycle state or contact the controller."
	scope := openAPIUUIDParameter("scope_id")
	scope["in"], scope["required"] = "query", false
	list["parameters"] = []any{
		scope,
		map[string]any{"name": "owner_agent_id", "in": "query", "required": false, "description": "Exact process owner, including for retained lost or closed sessions.", "schema": map[string]any{"type": "string", "pattern": `^[a-z0-9]+(-[a-z0-9]+)*-agent-[0-9a-f]{8}$`, "maxLength": 63}},
	}
	list["responses"].(map[string]any)["400"] = endpoints.OpenAPIResponse("invalid_query, invalid_scope_id or invalid_owner_agent_id", endpoints.OpenAPIRef("Error"))
	list["responses"].(map[string]any)["503"] = endpoints.OpenAPIResponse("sessions_unavailable", endpoints.OpenAPIRef("Error"))
	paths["/api/v1/sessions"] = map[string]any{"get": list}
	get := endpoints.OpenAPIQueryOperation("getRuntimeSession", "Get a durable runtime session by ID", "Sessions", endpoints.OpenAPIRef("RuntimeSession"))
	get["description"] = "Returns the retained session identity, owner and lifecycle without requiring a scope or owner argument. Reads do not change saved state. Query parameters are not accepted."
	get["parameters"] = []any{openAPIUUIDParameter("session_id")}
	get["responses"].(map[string]any)["400"] = endpoints.OpenAPIResponse("invalid_session_id or invalid_query", endpoints.OpenAPIRef("Error"))
	get["responses"].(map[string]any)["404"] = endpoints.OpenAPIResponse("session_not_found", endpoints.OpenAPIRef("Error"))
	get["responses"].(map[string]any)["503"] = endpoints.OpenAPIResponse("sessions_unavailable", endpoints.OpenAPIRef("Error"))
	paths["/api/v1/sessions/{session_id}"] = map[string]any{"get": get}
	paths["/api/v1/sessions/control"] = map[string]any{"post": map[string]any{
		"operationId": "controlRuntimeSession", "summary": "Inspect, verify, close, or abandon a lost session", "tags": []string{"Sessions"},
		"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": endpoints.OpenAPIRef("ControlSessionRequest")}}},
		"responses": map[string]any{
			"200": endpoints.OpenAPIResponse("Durable session view", endpoints.OpenAPIRef("RuntimeSession")),
			"400": endpoints.OpenAPIResponse("Invalid control request", endpoints.OpenAPIRef("Error")),
			"422": endpoints.OpenAPIResponse("Control rejected; a failed close retains its closing intent for reconciliation", endpoints.OpenAPIRef("Error")),
			"503": endpoints.OpenAPIResponse("session_control_uncertain or sessions_unavailable; query before retrying", endpoints.OpenAPIRef("Error")),
		},
	}}
}
