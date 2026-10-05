package operation

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
