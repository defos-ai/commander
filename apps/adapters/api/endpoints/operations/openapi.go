package operations

import (
	"fmt"
	"reflect"

	"commander/adapters/api/endpoints"
	"commander/framework/domain/operations"

	"github.com/google/jsonschema-go/jsonschema"
)

const openAPIUUIDPattern = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"

// The options must already contain the common API schemas, including Mission.
func RegisterOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
	for typeOf, schema := range map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[operations.Status](): {Type: "string", Enum: []any{
			operations.Planning, operations.Running, operations.Waiting, operations.Completed, operations.Failed, operations.Cancelled,
		}},
		reflect.TypeFor[operations.TaskStatus](): {Type: "string", Enum: []any{
			operations.TaskQueued, operations.TaskRunning, operations.TaskSucceeded, operations.TaskFailed, operations.TaskCancelled, operations.TaskInterrupted,
		}},
		reflect.TypeFor[operations.QuestionStatus](): {Type: "string", Enum: []any{
			operations.QuestionPending, operations.QuestionAnswered, operations.QuestionExpired,
		}},
		reflect.TypeFor[operations.ContinuationStatus](): {Type: "string", Enum: []any{
			operations.ContinuationPending, operations.ContinuationWaiting, operations.ContinuationResolved,
		}},
		reflect.TypeFor[operations.DecisionBy](): {Type: "string", Enum: []any{
			operations.DecisionCode, operations.DecisionModel, operations.DecisionUser,
		}},
	} {
		options.TypeSchemas[typeOf] = schema
	}
	for _, model := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"Task", reflect.TypeFor[operations.Task]()},
		{"Event", reflect.TypeFor[operations.Event]()},
		{"Question", reflect.TypeFor[operations.Question]()},
		{"Operation", reflect.TypeFor[operations.Operation]()},
	} {
		schema, err := jsonschema.ForType(model.typeOf, options)
		if err != nil {
			return fmt.Errorf("describe API %s: %w", model.name, err)
		}
		components[model.name] = schema
		options.TypeSchemas[model.typeOf] = schema
	}
	openAPIOperationPaths(paths)
	openAPICommandPaths(paths, components)
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
			description: "Returns all full saved operation records without pagination, ordered by created_at descending, then ID ascending for ties.",
			schema:      openAPIItems("Operation"),
		},
		{
			path: operation, id: "getOperation", summary: "Get an operation",
			description: "Returns the full saved operation, including its mission, preflight assessment, tasks, questions, output and events.",
			schema:      endpoints.OpenAPIRef("Operation"), parameters: []any{openAPIUUIDParameter("operation_id")},
			invalid: "invalid_operation_id", missing: "operation_not_found",
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
			description: "items contains the complete saved question history in saved order. last_question is the newest pending question that is not overdue (greatest asked_at, then greatest ID for ties), or null when none is eligible or the operation is terminal. remaining_questions counts other eligible pending questions, excluding last_question. The projection never expires questions or changes persisted state.",
			schema:      openAPIQuestions(), parameters: []any{openAPIUUIDParameter("operation_id")},
			invalid: "invalid_operation_id", missing: "operation_not_found",
		},
		{
			path: operation + "/questions/{question_id}", id: "getOperationQuestion", summary: "Get an operation question",
			description: "Returns the full saved question with its saved status and answer, even when it is overdue or the operation is terminal.",
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

func openAPICommandPaths(paths, components map[string]any) {
	components["StartOperationRequest"] = map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"mission_name", "input"},
		"properties": map[string]any{
			"operation_id": map[string]any{
				"type": "string", "format": "uuid", "pattern": "^" + openAPIUUIDPattern + "$",
				"description": "Optional canonical lowercase UUID. Omit to generate an ID. Reusing an existing ID returns operation_conflict; this is not an idempotency key.",
			},
			"mission_name": map[string]any{"type": "string", "minLength": 1, "pattern": `\S`},
			"input": map[string]any{
				"description": "Required JSON value, including null, arrays and scalars. The controller validates it against the selected mission's input schema.",
			},
		},
	}
	components["AssignOperationTaskRequest"] = map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"action_name"},
		"properties": map[string]any{
			"action_name": map[string]any{"type": "string", "minLength": 1, "pattern": `\S`},
			"input":       map[string]any{"type": "object", "description": "Optional task input object. Explicit null is invalid."},
			"step_id":     map[string]any{"type": "string", "description": "Optional mission step ID. An unknown step returns invalid_input."},
		},
	}
	components["CancelOperationRequest"] = map[string]any{
		"type": "object", "additionalProperties": false,
	}
	version := map[string]any{"type": "integer", "minimum": 1, "maximum": ^uint64(0)}
	components["AskOperationQuestionRequest"] = map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"text"},
		"dependentRequired": map[string][]string{"task_id": {"version"}, "version": {"task_id"}},
		"properties": map[string]any{
			"text": map[string]any{"type": "string", "minLength": 1, "pattern": `\S`},
			"options": map[string]any{
				"type": "array", "maxItems": 4,
				"items": map[string]any{"type": "string", "minLength": 1, "pattern": `\S`},
			},
			"timeout_seconds": map[string]any{
				"type": "integer", "minimum": 0, "maximum": 86400, "default": 120,
				"description": "Question lifetime in seconds. Omit or use 0 for the 120-second default; otherwise use 1 through 86400.",
			},
			"step_id": map[string]any{"type": "string", "description": "Optional mission step ID. An unknown step returns invalid_input."},
			"task_id": map[string]any{
				"type": "string", "format": "uuid", "pattern": "^" + openAPIUUIDPattern + "$",
				"description": "Optional task whose pending continuation requires this question. Must be supplied together with version.",
			},
			"version": version,
		},
	}
	components["AnswerOperationQuestionRequest"] = map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"answer"},
		"properties": map[string]any{"answer": map[string]any{"type": "string", "minLength": 1, "pattern": `\S`}},
	}
	components["NextAction"] = map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"action_name"},
		"properties": map[string]any{
			"action_name": map[string]any{"type": "string", "minLength": 1, "pattern": `\S`},
			"input":       map[string]any{"type": "object", "description": "Optional task input object. Explicit null is invalid."},
			"reason":      map[string]any{"type": "string"},
		},
	}
	components["ResolveOperationContinuationRequest"] = map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"version", "actions"},
		"properties": map[string]any{
			"version": version,
			"actions": map[string]any{
				"type": "array", "items": endpoints.OpenAPIRef("NextAction"),
				"description": "Required explicit follow-up actions. An empty array resolves the continuation without assigning tasks.",
			},
		},
	}
	const root = "/api/v1/operations"
	const operation = root + "/{operation_id}"
	const commandDescription = " Commands go to the controller over the bus with a 10-second request timeout and no automatic retry. A timeout, operation_write_uncertain or command_outcome_unknown can follow accepted work; query saved state before resubmitting."
	for _, route := range []struct {
		path, id, summary, description, request, response, status, missing, conflict, location string
		bodyRequired, invalidInput                                                             bool
		resourceID                                                                             string
	}{
		{
			path: root, id: "startOperation", summary: "Start an operation",
			description: "Starts the named mission and returns the full created operation. mission_name must be nonblank and input must be present. Repeating a supplied operation_id returns 409 operation_conflict.",
			request:     "StartOperationRequest", response: "Operation", status: "201", missing: "mission_not_found", conflict: "operation_conflict",
			location: "/api/v1/operations/{operation_id}", bodyRequired: true, invalidInput: true,
		},
		{
			path: operation + "/tasks", id: "assignOperationTask", summary: "Assign an operation task",
			description: "Returns the full assigned task. action_name must be nonblank. The path supplies operation_id; operation_id and agent_id are not accepted in the body. The controller selects the agent, and an unknown action can remain queued. Repeated assignment creates another task.",
			request:     "AssignOperationTaskRequest", response: "Task", status: "201", missing: "operation_not_found", conflict: "operation_conflict, operation_finished or question_pending",
			location: "/api/v1/operations/{operation_id}/tasks/{task_id}", bodyRequired: true, invalidInput: true,
		},
		{
			path: operation + "/cancel", id: "cancelOperation", summary: "Cancel an operation",
			description: "Cancels an operation and returns the full updated operation. Queued tasks are cancelled; already dispatched work may still finish. The body may be empty or {} only. Cancelling an already finished or cancelled operation returns 409 operation_finished.",
			request:     "CancelOperationRequest", response: "Operation", status: "200", missing: "operation_not_found", conflict: "operation_conflict or operation_finished",
		},
		{
			path: operation + "/questions", id: "askOperationQuestion", summary: "Ask an operation question",
			description: "Creates a question and returns the full saved question. text and every option must be nonblank, with at most four options. Omitted or zero timeout_seconds means 120 seconds. Link a pending continuation by supplying both task_id and its current positive version. The path supplies operation_id.",
			request:     "AskOperationQuestionRequest", response: "Question", status: "201", missing: "operation_not_found or task_not_found (linked question)", conflict: "operation_conflict, operation_finished or continuation_conflict",
			location: "/api/v1/operations/{operation_id}/questions/{question_id}", bodyRequired: true, invalidInput: true,
		},
		{
			path: operation + "/questions/{question_id}/answer", id: "answerOperationQuestion", summary: "Answer an operation question",
			description: "Records guidance for a pending question and returns the full updated operation. answer must be nonblank; free-text answers need not match a listed option. Answering a linked question returns its continuation to model review; it does not queue selected actions. Expiry likewise returns the decision to review without inventing an answer. An answered or expired question cannot be answered again. The path supplies operation_id and question_id.",
			request:     "AnswerOperationQuestionRequest", response: "Operation", status: "200", missing: "operation_not_found or question_not_found", conflict: "operation_conflict, operation_finished, question_answered or question_expired",
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
		if route.invalidInput {
			responses["422"] = endpoints.OpenAPIResponse("invalid_input", endpoints.OpenAPIRef("Error"))
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
			"schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "default": 100},
		},
	}
}
