package operations

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain"

	"github.com/google/jsonschema-go/jsonschema"
)

const openAPIUUIDPattern = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"

// The options must already contain the common API schemas, including Mission.
func RegisterOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
	for _, model := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"AgentReadiness", reflect.TypeFor[models.AgentReadinessResponse]()},
		{"ActionReadiness", reflect.TypeFor[models.ActionReadinessResponse]()},
		{"Preflight", reflect.TypeFor[models.PreflightResponse]()},
		{"Affinity", reflect.TypeFor[models.AffinityResponse]()},
		{"Continuation", reflect.TypeFor[models.ContinuationResponse]()},
		{"Task", reflect.TypeFor[models.TaskResponse]()},
		{"Event", reflect.TypeFor[models.EventResponse]()},
		{"Log", reflect.TypeFor[models.LogResponse]()},
		{"Question", reflect.TypeFor[models.QuestionResponse]()},
		{"OperationProgress", reflect.TypeFor[models.OperationProgressResponse]()},
		{"Operation", reflect.TypeFor[models.OperationResponse]()},
		{"GraphTask", reflect.TypeFor[models.GraphTaskResponse]()},
		{"GraphArtifact", reflect.TypeFor[models.GraphArtifactResponse]()},
		{"GraphFile", reflect.TypeFor[models.GraphFileResponse]()},
		{"GraphNode", reflect.TypeFor[models.GraphNodeResponse]()},
		{"GraphEdge", reflect.TypeFor[models.GraphEdgeResponse]()},
		{"OperationGraph", reflect.TypeFor[models.OperationGraphResponse]()},
	} {
		schema, err := jsonschema.ForType(model.typeOf, options)
		if err != nil {
			return fmt.Errorf("describe API %s: %w", model.name, err)
		}
		switch model.name {
		case "AgentReadiness":
			schema.Properties["agent_type"].Description = "Registered agent type in this saved readiness snapshot."
			schema.Properties["missing_requirements"].Description = "Missing prerequisites assessed on a live ready/working candidate. Entries with kind=secret identify declared shared secret names; agent_type identifies the requesting candidate, not a secret scope. Sensitive host configuration is separate. Stale or offline candidates report only their unavailability reason. Values and secret lease refresh requests are never included."
		case "Preflight":
			schema.Description = "Saved readiness for mission, step and same-agent actions. Before affinity binds, grouped candidates must advertise every grouped action; afterward only the bound agent satisfies grouped coverage. Capacity is assessed separately by the scheduler. Resolving a missing secret alone does not guarantee readiness when other requirements remain."
		case "Affinity":
			schema.Description = "Read-only same-agent binding derived from the saved mission and task assignments, including terminal tasks. Absent without the rule. This is a placement constraint, not execution ordering or proof of agent availability."
			schema.Properties["for_actions"].Description = "Actions bound to one agent instance within this operation."
			schema.Properties["agent_id"].Description = "Agent selected by the first matching dispatch. Later matching tasks wait for this agent; no automatic fallback or rebinding. Absent before dispatch or when error is present."
			schema.Properties["error"].Description = "Saved rule or dispatch history is inconsistent. Absence of agent_id in this case does not mean the group is unbound; inspect the saved tasks."
		case "Continuation":
			schema.Properties["status"].Enum = []any{"pending", "waiting", "resolved"}
			schema.Properties["decision_by"].Enum = []any{"code", "model", "user"}
		case "Task", "GraphTask":
			schema.Properties["status"].Enum = []any{"queued", "running", "succeeded", "failed", "cancelled", "interrupted"}
			if model.name == "Task" {
				schema.Properties["agent_type"].Description = "Registered type of the assigned agent reporting this live execution wait; present only with missing_requirements."
				schema.Properties["missing_requirements"].Description = "Current secret names reported by matching live running work before its saved deadline. Values are never included. These read-only requirements may change without updated_at changing; they neither alter mission preflight nor extend the task deadline."
			}
		case "Question":
			schema.Properties["fallback_at"].Description = "Earliest time a model may answer. Operators may answer immediately, and unanswered questions remain pending after this time."
			schema.Properties["status"].Enum = []any{"pending", "answered"}
			schema.Properties["answered_by"].Enum = []any{"operator", "model"}
			schema.Properties["answered_by"].Description = "Answer origin assigned by the submitting adapter: operator for the HTTP API, model for MCP. Absent until an answer is saved."
		case "OperationProgress":
			schema.Description = "Read-only progress derived from this saved operation snapshot at one assessment time. Never persisted or used to infer agent idleness."
			schema.Properties["task_counts"].Type = "object"
			schema.Properties["task_counts"].Types = nil
			schema.Properties["task_counts"].Description = "Counts by saved task status. Statuses with no tasks are omitted; an empty operation returns {}."
			schema.Properties["finished_tasks"].Description = "Tasks with known outcomes: succeeded, failed or cancelled. Interrupted tasks remain unfinished even when finished_at is set."
			schema.Properties["pending_decisions"].Description = "Pending model continuations; zero for terminal operations."
			schema.Properties["pending_questions"].Description = "Pending unanswered questions, including after fallback_at; zero for terminal operations."
			schema.Properties["settled"].Description = "True only when the operation is terminal and every saved task has a known finished outcome. Keep polling while false, including after cancellation or failure."
		case "Operation", "Event", "OperationGraph":
			schema.Properties["status"].Enum = []any{"planning", "running", "waiting", "completed", "failed", "cancelled"}
			if model.name == "Operation" {
				schema.Properties["affinity"].Description = "Derived same-agent placement. Queued tasks remain unassigned until dispatch; affinity does not change their agent_id."
			}
		case "GraphNode":
			schema.Properties["kind"].Enum = []any{"task", "artifact", "file"}
			for _, variant := range []struct{ kind, model string }{{"task", "GraphTask"}, {"artifact", "GraphArtifact"}, {"file", "GraphFile"}} {
				properties := map[string]*jsonschema.Schema{
					"kind": {Enum: []any{variant.kind}},
					"task": {Not: &jsonschema.Schema{}}, "artifact": {Not: &jsonschema.Schema{}}, "file": {Not: &jsonschema.Schema{}},
				}
				properties[variant.kind] = components[variant.model].(*jsonschema.Schema)
				schema.OneOf = append(schema.OneOf, &jsonschema.Schema{Required: []string{variant.kind}, Properties: properties})
			}
		case "GraphEdge":
			schema.Properties["kind"].Enum = []any{"parent_child", "produced", "contains"}
		}
		if model.name == "OperationGraph" {
			for _, field := range []string{"nodes", "edges"} {
				schema.Properties[field].Type = "array"
				schema.Properties[field].Types = nil
			}
		}
		components[model.name] = schema
		options.TypeSchemas[model.typeOf] = schema
	}
	if err := openAPICommandSchemas(components); err != nil {
		return err
	}
	openAPIOperationPaths(paths)
	openAPILogPaths(paths)
	openAPICommandPaths(paths)
	return nil
}

func openAPIOperationPaths(paths map[string]any) {
	const root = "/api/v1/operations"
	const operation = root + "/{operation_id}"
	for _, route := range []struct {
		path, id, summary, description, invalid, missing string
		schema                                           any
		parameters                                       []any
	}{
		{
			path: root, id: "listOperations", summary: "List operations",
			description: "Returns all full saved operation records without pagination, ordered by created_at descending, then ID ascending for ties. Active running tasks may include live agent_type and missing_requirements for their exact execution; this read-only observation does not extend deadlines.",
			schema:      openAPIItems("Operation"),
		},
		{
			path: operation, id: "getOperation", summary: "Get an operation",
			description: "Returns the full saved operation, including its mission, preflight assessment, tasks, questions, output and events. Active running tasks may include live agent_type and missing_requirements for their exact execution; this read-only observation does not extend deadlines.",
			schema:      endpoints.OpenAPIRef("Operation"), parameters: []any{openAPIUUIDParameter("operation_id")},
			invalid: "invalid_operation_id", missing: "operation_not_found",
		},
		{
			path: operation + "/graph", id: "getOperationGraph", summary: "Get an operation execution graph",
			description: "Returns an immediate graph snapshot of saved tasks and their result artifact/file descriptors. Includes queued, running and finished tasks; proposed continuation actions are excluded. Clients may poll every 2–5 seconds. Keep polling terminal operations while tasks remain queued, running or interrupted, since dispatched work can finish later. Nodes have stable IDs: task:<task_id>, artifact:<artifact_id>, file:<artifact_id>:<file_id>. Each node has exactly one payload matching kind. Edges are unique by source, target and kind: parent_child, produced or contains. Parent edges require both distinct task nodes; parent_task_id retains the saved relationship even if its edge is omitted. Tasks appear in saved order, followed by artifact/file nodes in first-encounter order; arrays are empty rather than null. File descriptors appear after task results are saved and do not verify that bytes are downloadable. This endpoint reads only the operation store and never executes work.",
			schema:      endpoints.OpenAPIRef("OperationGraph"), parameters: []any{openAPIUUIDParameter("operation_id")},
			invalid: "invalid_operation_id", missing: "operation_not_found",
		},
		{
			path: operation + "/report", id: "getOperationReport", summary: "Get an operation report",
			description: "Reads the completed operation's report_id and returns the full saved report from the report store, without contacting the controller. A noncompleted operation, absent report reference or missing report returns report_not_found. A report whose operation or mission differs from the operation, or corrupt saved data, returns internal_error.",
			schema:      endpoints.OpenAPIRef("Report"), parameters: []any{openAPIUUIDParameter("operation_id")},
			invalid: "invalid_operation_id", missing: "operation_not_found or report_not_found",
		},
		{
			path: operation + "/tasks", id: "listOperationTasks", summary: "List operation tasks",
			description: "Returns full tasks in assignment order, strictly after the task identified by cursor. An omitted or empty cursor starts the first page. next_cursor is the last returned task ID, or the request cursor unchanged on an empty page (including an initial empty string). Unknown or duplicate query parameters and malformed query encoding are rejected.",
			schema:      openAPIPage("Task"), parameters: openAPIPageParameters(false),
			invalid: "invalid_operation_id, invalid_query, invalid_cursor or invalid_limit", missing: "operation_not_found or task_not_found (unknown task cursor)",
		},
		{
			path: operation + "/tasks/{task_id}", id: "getOperationTask", summary: "Get an operation task",
			description: "Returns the full saved task, including any result, artifacts and continuation.",
			schema:      endpoints.OpenAPIRef("Task"), parameters: []any{openAPIUUIDParameter("operation_id"), openAPIUUIDParameter("task_id")},
			invalid: "invalid_operation_id or invalid_task_id", missing: "operation_not_found or task_not_found",
		},
		{
			path: operation + "/events", id: "listOperationEvents", summary: "List operation events",
			description: "Returns saved events in ascending sequence order, strictly after cursor. An omitted or empty cursor starts before the first event. next_cursor is the last returned sequence as a decimal string, or the request cursor unchanged on an empty page (including an initial empty string). A cursor at or beyond the latest event returns an empty page. Unknown or duplicate query parameters and malformed query encoding are rejected.",
			schema:      openAPIPage("Event"), parameters: openAPIPageParameters(true),
			invalid: "invalid_operation_id, invalid_query, invalid_cursor or invalid_limit", missing: "operation_not_found",
		},
		{
			path: operation + "/questions", id: "listOperationQuestions", summary: "List operation questions",
			description: "items contains the complete saved question history in saved order. last_question is the newest pending question (greatest asked_at, then greatest ID for ties), or null when no question is pending or the operation is terminal. remaining_questions counts other pending questions, excluding last_question. Passing fallback_at permits model answers without closing the question or changing persisted state.",
			schema:      openAPIQuestions(), parameters: []any{openAPIUUIDParameter("operation_id")},
			invalid: "invalid_operation_id", missing: "operation_not_found",
		},
		{
			path: operation + "/questions/{question_id}", id: "getOperationQuestion", summary: "Get an operation question",
			description: "Returns the full saved question with its saved status and answer, including after fallback_at or when the operation is terminal.",
			schema:      endpoints.OpenAPIRef("Question"), parameters: []any{openAPIUUIDParameter("operation_id"), openAPIUUIDParameter("question_id")},
			invalid: "invalid_operation_id or invalid_question_id", missing: "operation_not_found or question_not_found",
		},
	} {
		endpoint := endpoints.OpenAPIQueryOperation(route.id, route.summary, "Operations", route.schema)
		endpoint["description"] = route.description
		if len(route.parameters) > 0 {
			endpoint["parameters"] = route.parameters
		}
		responses := endpoint["responses"].(map[string]any)
		if route.invalid != "" {
			responses["400"] = endpoints.OpenAPIResponse(route.invalid+" or invalid_request_origin", endpoints.OpenAPIRef("Error"))
		}
		if route.missing != "" {
			responses["404"] = endpoints.OpenAPIResponse(route.missing, endpoints.OpenAPIRef("Error"))
		}
		paths[route.path] = map[string]any{"get": endpoint}
	}
}

// Request fields and requiredness come from the HTTP models. Constraints describe
// the accepted command values without narrowing arbitrary mission input/output.
func openAPICommandSchemas(components map[string]any) error {
	options := &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[json.RawMessage](): {},
	}}
	nonblank := map[string]any{"minLength": 1, "pattern": `\S`}
	version := map[string]any{"minimum": 1, "maximum": ^uint64(0)}
	taskInput := map[string]any{"type": "object", "description": "Optional task input object. Explicit null is invalid."}
	stepID := map[string]any{"description": "Optional mission step ID. An unknown step returns invalid_input."}
	for _, model := range []struct {
		name        string
		typeOf      reflect.Type
		constraints map[string]map[string]any
	}{
		{
			name: "StartOperationRequest", typeOf: reflect.TypeFor[models.StartOperationRequest](),
			constraints: map[string]map[string]any{
				"operation_id": {
					"format": "uuid", "pattern": "^" + openAPIUUIDPattern + "$",
					"description": "Optional canonical lowercase UUID. Omit to generate an ID. Reusing an existing ID returns operation_conflict; this is not an idempotency key.",
				},
				"mission_name": nonblank,
				"input":        {"description": "Required JSON value, including null, arrays and scalars. The controller validates it against the selected mission's input schema."},
			},
		},
		{
			name: "AssignOperationTaskRequest", typeOf: reflect.TypeFor[models.AssignTaskRequest](),
			constraints: map[string]map[string]any{
				"action_name": nonblank, "input": taskInput, "step_id": stepID,
			},
		},
		{name: "CancelOperationRequest", typeOf: reflect.TypeFor[models.CancelOperationRequest]()},
		{
			name: "CompleteOperationRequest", typeOf: reflect.TypeFor[models.CompleteOperationRequest](),
			constraints: map[string]map[string]any{
				"output": {"description": "Required JSON value, including null, arrays and scalars. The controller validates it against the operation mission's output schema."},
			},
		},
		{
			name: "FailOperationRequest", typeOf: reflect.TypeFor[models.FailOperationRequest](),
			constraints: map[string]map[string]any{"reason": nonblank},
		},
		{
			name: "AskOperationQuestionRequest", typeOf: reflect.TypeFor[models.AskQuestionRequest](),
			constraints: map[string]map[string]any{
				"text": nonblank,
				"options": {
					"type": "array", "maxItems": 4,
					"items": map[string]any{"type": "string", "minLength": 1, "pattern": `\S`},
				},
				"timeout_seconds": {
					"minimum": 0, "maximum": 86400, "default": 120,
					"description": "Operator waiting window in seconds. Omit or use 0 for the 120-second default; otherwise use 1 through 86400. The question remains pending afterward; model answers become available at fallback_at.",
				},
				"step_id": stepID,
				"task_id": {
					"format": "uuid", "pattern": "^" + openAPIUUIDPattern + "$",
					"description": "Optional task whose pending continuation requires this question. Must be supplied together with version.",
				},
				"version": version,
			},
		},
		{
			name: "AnswerOperationQuestionRequest", typeOf: reflect.TypeFor[models.AnswerQuestionRequest](),
			constraints: map[string]map[string]any{"answer": nonblank},
		},
		{
			name: "NextAction", typeOf: reflect.TypeFor[models.NextActionRequest](),
			constraints: map[string]map[string]any{"action_name": nonblank, "input": taskInput},
		},
		{
			name: "ResolveOperationContinuationRequest", typeOf: reflect.TypeFor[models.ResolveContinuationRequest](),
			constraints: map[string]map[string]any{
				"version": version,
				"actions": {
					"type": "array", "items": endpoints.OpenAPIRef("NextAction"),
					"description": "Required explicit follow-up actions. An empty array resolves the continuation without assigning tasks.",
				},
			},
		},
	} {
		inferred, err := jsonschema.ForType(model.typeOf, options)
		if err != nil {
			return fmt.Errorf("describe API %s: %w", model.name, err)
		}
		// Apply numeric bounds as JSON values so the uint64 maximum stays exact.
		data, err := json.Marshal(inferred)
		if err != nil {
			return fmt.Errorf("encode API %s schema: %w", model.name, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			return fmt.Errorf("decode API %s schema: %w", model.name, err)
		}
		properties, _ := schema["properties"].(map[string]any)
		for name, constraints := range model.constraints {
			property, ok := properties[name].(map[string]any)
			if !ok && properties[name] == true {
				property = make(map[string]any)
				properties[name] = property
				ok = true
			}
			if !ok {
				return fmt.Errorf("describe API %s: missing property %s", model.name, name)
			}
			for keyword, value := range constraints {
				property[keyword] = value
			}
		}
		if model.name == "AskOperationQuestionRequest" {
			schema["dependentRequired"] = map[string][]string{"task_id": {"version"}, "version": {"task_id"}}
		}
		components[model.name] = schema
	}
	return nil
}

func openAPICommandPaths(paths map[string]any) {
	const root = "/api/v1/operations"
	const operation = root + "/{operation_id}"
	const commandDescription = " Commands go to the controller over the bus with a 10-second request timeout and no automatic retry. A timeout, operation_write_uncertain or command_outcome_unknown can follow accepted work; query saved state before resubmitting."
	for _, route := range []struct {
		path, id, summary, description, request, response, status, missing, conflict, location string
		bodyRequired                                                                           bool
		resourceID, validationError                                                            string
	}{
		{
			path: root, id: "startOperation", summary: "Start an operation",
			description: "Starts the named mission and returns the full created operation. mission_name must be nonblank and input must be present. Repeating a supplied operation_id returns 409 operation_conflict.",
			request:     "StartOperationRequest", response: "Operation", status: "201", missing: "mission_not_found", conflict: "operation_conflict",
			location: "/api/v1/operations/{operation_id}", bodyRequired: true, validationError: "invalid_input",
		},
		{
			path: operation + "/tasks", id: "assignOperationTask", summary: "Assign an operation task",
			description: "Returns the full assigned task. action_name must be nonblank. The path supplies operation_id; operation_id and agent_id are not accepted in the body. The controller selects the agent, and an unknown action can remain queued. Repeated assignment creates another task.",
			request:     "AssignOperationTaskRequest", response: "Task", status: "201", missing: "operation_not_found", conflict: "operation_conflict, operation_finished or question_pending",
			location: "/api/v1/operations/{operation_id}/tasks/{task_id}", bodyRequired: true, validationError: "invalid_input",
		},
		{
			path: operation + "/cancel", id: "cancelOperation", summary: "Cancel an operation",
			description: "Cancels an operation and returns the full updated operation. Queued tasks are cancelled; already dispatched work may still finish. The body may be empty or {} only. Cancelling an already finished or cancelled operation returns 409 operation_finished.",
			request:     "CancelOperationRequest", response: "Operation", status: "200", missing: "operation_not_found", conflict: "operation_conflict or operation_finished",
		},
		{
			path: operation + "/complete", id: "completeOperation", summary: "Complete an operation",
			description: "Completes an operation and returns the full updated operation with report_id. output must be present and match the mission's output schema; null is accepted when that schema allows it. Queued or running tasks, pending questions, and unresolved automatic or operator continuations block completion. Pending model continuations are resolved during completion. The report is saved before the operation is marked completed. The path supplies operation_id; it is not accepted in the body.",
			request:     "CompleteOperationRequest", response: "Operation", status: "200", missing: "operation_not_found", conflict: "operation_conflict, operation_finished, operation_active_tasks, question_pending or operation_unresolved_work",
			bodyRequired: true, validationError: "invalid_output",
		},
		{
			path: operation + "/fail", id: "failOperation", summary: "Fail an operation",
			description: "Fails an operation and returns the full updated operation. reason must be a nonblank string. Queued tasks are cancelled; already dispatched work may still finish. No report is created. The path supplies operation_id; it is not accepted in the body.",
			request:     "FailOperationRequest", response: "Operation", status: "200", missing: "operation_not_found", conflict: "operation_conflict or operation_finished",
			bodyRequired: true,
		},
		{
			path: operation + "/questions", id: "askOperationQuestion", summary: "Ask an operation question",
			description: "Creates a question and returns the full saved question. text and every option must be nonblank, with at most four options. Omitted or zero timeout_seconds gives the operator a 120-second waiting window. Operators can answer immediately; models can answer at or after fallback_at. Unanswered questions remain pending and keep their continuation blocked. Link a pending continuation by supplying both task_id and its current positive version. The path supplies operation_id.",
			request:     "AskOperationQuestionRequest", response: "Question", status: "201", missing: "operation_not_found or task_not_found (linked question)", conflict: "operation_conflict, operation_finished or continuation_conflict",
			location: "/api/v1/operations/{operation_id}/questions/{question_id}", bodyRequired: true, validationError: "invalid_input",
		},
		{
			path: operation + "/questions/{question_id}/answer", id: "answerOperationQuestion", summary: "Answer an operation question",
			description: "Records an operator answer for a pending question and returns the full updated operation. Operators may answer before or after fallback_at. answer must be nonblank; free-text answers need not match a listed option. Answering a linked question returns its continuation to model review; it does not queue selected actions. Passing fallback_at never invents an answer or releases the continuation. An answered question cannot be answered again. The path supplies operation_id and question_id.",
			request:     "AnswerOperationQuestionRequest", response: "Operation", status: "200", missing: "operation_not_found or question_not_found", conflict: "operation_conflict, operation_finished, question_answered or question_operator_waiting",
			bodyRequired: true, resourceID: "question_id",
		},
		{
			path: operation + "/tasks/{task_id}/continuation/resolve", id: "resolveOperationContinuation", summary: "Resolve an operation task continuation",
			description: "Resolves a task's pending continuation at the supplied positive version and returns the full updated operation. actions must be an explicit array; [] resolves without follow-up work. Each action requires a nonblank action_name, with optional object input and string reason. The controller selects agents; agent_id is not accepted. The path supplies operation_id and task_id.",
			request:     "ResolveOperationContinuationRequest", response: "Operation", status: "200", missing: "operation_not_found or task_not_found", conflict: "operation_conflict, operation_finished, continuation_conflict, question_pending or task_limit_exceeded",
			bodyRequired: true, resourceID: "task_id",
		},
	} {
		endpoint := endpoints.OpenAPIQueryOperation(route.id, route.summary, "Operations", endpoints.OpenAPIRef(route.response))
		endpoint["description"] = route.description + commandDescription
		endpoint["requestBody"] = map[string]any{
			"required":    route.bodyRequired,
			"description": "At most 1 MiB. Nonempty bodies require application/json; charset parameters are accepted. Unknown properties, invalid JSON and invalid body fields return invalid_request.",
			"content":     map[string]any{"application/json": map[string]any{"schema": endpoints.OpenAPIRef(route.request)}},
		}
		responses := endpoint["responses"].(map[string]any)
		delete(responses, "200")
		success := endpoints.OpenAPIResponse("Success", endpoints.OpenAPIRef(route.response))
		if route.location != "" {
			success["headers"] = map[string]any{"Location": map[string]any{
				"description": "Relative URL of the created resource: " + route.location, "required": true,
				"schema": map[string]any{"type": "string", "format": "uri-reference"},
			}}
		}
		responses[route.status] = success
		invalid := "invalid_request or invalid_request_origin"
		if route.path != root {
			endpoint["parameters"] = []any{openAPIUUIDParameter("operation_id")}
			invalid = "invalid_operation_id, " + invalid
		}
		if route.resourceID != "" {
			endpoint["parameters"] = append(endpoint["parameters"].([]any), openAPIUUIDParameter(route.resourceID))
			invalid = "invalid_" + route.resourceID + ", " + invalid
		}
		responses["400"] = endpoints.OpenAPIResponse(invalid, endpoints.OpenAPIRef("Error"))
		responses["404"] = endpoints.OpenAPIResponse(route.missing, endpoints.OpenAPIRef("Error"))
		responses["409"] = endpoints.OpenAPIResponse(route.conflict, endpoints.OpenAPIRef("Error"))
		responses["413"] = endpoints.OpenAPIResponse("request_too_large", endpoints.OpenAPIRef("Error"))
		responses["415"] = endpoints.OpenAPIResponse("unsupported_media_type", endpoints.OpenAPIRef("Error"))
		if route.validationError != "" {
			responses["422"] = endpoints.OpenAPIResponse(route.validationError, endpoints.OpenAPIRef("Error"))
		}
		responses["503"] = endpoints.OpenAPIResponse("operation_write_uncertain, command_outcome_unknown, controller_unavailable or request_canceled. An uncertain write takes precedence over a concurrent request timeout; query saved state before resubmitting.", endpoints.OpenAPIRef("Error"))
		responses["504"] = endpoints.OpenAPIResponse("request_timeout. Work may already have been accepted; query saved state before resubmitting.", endpoints.OpenAPIRef("Error"))
		if paths[route.path] == nil {
			paths[route.path] = map[string]any{}
		}
		paths[route.path].(map[string]any)["post"] = endpoint
	}
}

func openAPIItems(model string) map[string]any {
	return map[string]any{
		"type": "object", "required": []string{"items"},
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": endpoints.OpenAPIRef(model)}},
	}
}

func openAPIPage(model string) map[string]any {
	schema := openAPIItems(model)
	schema["required"] = []string{"items", "next_cursor"}
	schema["properties"].(map[string]any)["next_cursor"] = map[string]any{
		"type": "string", "description": "Last returned item cursor; unchanged from the request cursor when no items are returned. Empty on an empty initial page.",
	}
	return schema
}

func openAPIQuestions() map[string]any {
	schema := openAPIItems("Question")
	schema["required"] = []string{"items", "last_question", "remaining_questions"}
	properties := schema["properties"].(map[string]any)
	properties["last_question"] = map[string]any{"anyOf": []any{endpoints.OpenAPIRef("Question"), map[string]any{"type": "null"}}}
	properties["remaining_questions"] = map[string]any{"type": "integer", "minimum": 0}
	return schema
}

func openAPIUUIDParameter(name string) map[string]any {
	return map[string]any{
		"name": name, "in": "path", "required": true, "description": "Canonical lowercase UUID.",
		"schema": map[string]any{"type": "string", "format": "uuid", "pattern": "^" + openAPIUUIDPattern + "$"},
	}
}

func openAPIPageParameters(events bool) []any {
	description := "Exclusive task ID cursor: a canonical lowercase UUID for a task belonging to this operation. Empty starts the first page."
	pattern := "^(" + openAPIUUIDPattern + ")?$"
	if events {
		description = "Exclusive event sequence cursor as a decimal unsigned 64-bit integer string (0 through 18446744073709551615). Empty starts the first page."
		pattern = "^[0-9]*$"
	}
	return []any{
		openAPIUUIDParameter("operation_id"),
		map[string]any{
			"name": "cursor", "in": "query", "required": false, "allowEmptyValue": true, "description": description,
			"schema": map[string]any{"type": "string", "pattern": pattern, "default": ""},
		},
		map[string]any{
			"name": "limit", "in": "query", "required": false, "description": "Maximum number of records. Must occur at most once and contain a decimal integer from 1 through 1000.",
			"schema": map[string]any{"type": "integer", "minimum": 1, "maximum": domain.MaxPageSize, "default": 100},
		},
	}
}

func openAPILogPaths(paths map[string]any) {
	endpoint := endpoints.OpenAPIQueryOperation("listOperationLogs", "List operation execution logs", "Operations", openAPIPage("Log"))
	endpoint["description"] = "Reads immutable execution observations without contacting the controller, including logs received after work ends. Optional task_id, agent_id and action_name filters intersect. task_id must identify a task belonging to this operation. order=asc (default) follows durable ingestion order; order=desc returns newest entries first. created_at and UUID order do not control sorting. A cursor must identify a log matching the operation and every filter. Pages continue strictly after that anchor in the requested direction. An omitted or empty cursor starts at the oldest matching log for asc or newest for desc. next_cursor is the last returned log ID, or the request cursor unchanged on an empty page. In desc order next_cursor loads older logs; to follow new logs, use the first/newest entry ID as cursor with order=asc and the same filters. Unknown, repeated, malformed or empty supplied filters/order are rejected."
	parameters := openAPIPageParameters(false)
	parameters[1].(map[string]any)["description"] = "Exclusive log ID cursor: a canonical lowercase UUID identifying a log matching this operation and every supplied filter. asc returns later appends; desc returns earlier appends. Empty starts in the requested direction."
	task := openAPIUUIDParameter("task_id")
	task["in"], task["required"] = "query", false
	task["description"] = "Exact task ID belonging to this operation; must not be empty."
	parameters = append(parameters, task,
		map[string]any{"name": "agent_id", "in": "query", "required": false, "description": "Exact agent runtime ID; must not be empty.",
			"schema": map[string]any{"type": "string", "pattern": `^[a-z0-9]+(-[a-z0-9]+)*-agent-[0-9a-f]{8}$`, "maxLength": 63}},
		map[string]any{"name": "action_name", "in": "query", "required": false, "description": "Exact action name, preserving whitespace and case. Must contain nonblank UTF-8 text.",
			"schema": map[string]any{"type": "string", "minLength": 1, "pattern": `\S`}},
		map[string]any{"name": "order", "in": "query", "required": false, "description": "Durable append order. asc returns oldest first and pages toward newer logs; desc returns newest first and pages toward older logs. Accepts exactly asc or desc.",
			"schema": map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "default": "asc"}},
	)
	endpoint["parameters"] = parameters
	responses := endpoint["responses"].(map[string]any)
	responses["400"] = endpoints.OpenAPIResponse("invalid_operation_id, invalid_query, invalid_cursor, invalid_limit or invalid_request_origin", endpoints.OpenAPIRef("Error"))
	responses["404"] = endpoints.OpenAPIResponse("operation_not_found, task_not_found or log_not_found (unknown or out-of-scope cursor)", endpoints.OpenAPIRef("Error"))
	paths["/api/v1/operations/{operation_id}/logs"] = map[string]any{"get": endpoint}
}
