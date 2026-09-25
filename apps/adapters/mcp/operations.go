package missionsmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	commands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type OperationRequest struct {
	OperationID string `json:"operation_id"`
}

// MCP input types reuse command fields. Validation runs in the command client so
// invalid domain input is returned as an action error, not a transport error.
type StartOperationRequest commands.StartOperationCommand
type AssignTaskRequest commands.AssignTaskCommand
type ResolveContinuationRequest commands.ResolveContinuationCommand
type AskQuestionRequest commands.AskQuestionCommand
type AnswerQuestionRequest commands.AnswerQuestionCommand
type CompleteOperationRequest commands.CompleteOperationCommand
type FailOperationRequest commands.FailOperationCommand
type OperationEventsRequest struct {
	OperationRequest
	AfterSequence uint64 `json:"after_sequence,omitempty"`
	WaitSeconds   int    `json:"wait_seconds,omitempty"`
}

func (r OperationEventsRequest) Validate() error {
	if r.WaitSeconds < 0 || r.WaitSeconds > 30 {
		return errors.New("wait_seconds must be between 0 and 30")
	}
	return nil
}

type OperationEventsResult struct {
	Events       []operations.Event `json:"events"`
	NextSequence uint64             `json:"next_sequence"`
}

func addOperationTools(server *mcp.Server, config Config) {
	execution := config.Operations
	addTool(server, "list_missions", "List reusable JSON mission definitions, automation rules, suggested steps, and input/output schemas. Does not execute work.", emptySchema,
		func(ctx context.Context, _ struct{}) (struct {
			Missions []missions.Mission `json:"missions"`
		}, error) {
			items, err := config.MissionStore.List(ctx)
			return struct {
				Missions []missions.Mission `json:"missions"`
			}{items}, err
		})
	addTool(server, "start_operation", "Start a tracked operation from a registered mission. Optionally supply operation_id as a canonical lowercase UUID to track it before the response arrives; otherwise one is generated. Existing IDs return a conflict; use get_operation to inspect them. Returns preflight coverage: planning when ready, waiting when requirements are missing. Tasks remain queued until all declared mission requirements are ready.", objectSchema(map[string]any{"operation_id": map[string]any{"type": "string", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"}, "mission_name": stringProperty, "input": map[string]any{}}, "mission_name", "input"),
		func(ctx context.Context, request StartOperationRequest) (operations.Operation, error) {
			return execution.Start(ctx, commands.StartOperationCommand(request))
		})
	addTool(server, "list_operations", "List operation IDs, mission names, inputs, status, task counts and counts of pending model decisions/questions. Use get_operation for results and decisions, or get_report for a final report.", emptySchema,
		func(ctx context.Context, _ struct{}) (struct {
			Operations []operationSummary `json:"operations"`
		}, error) {
			items, err := config.OperationStore.List(ctx)
			summaries := make([]operationSummary, 0, len(items))
			for _, item := range items {
				summaries = append(summaries, summarizeOperation(item))
			}
			return struct {
				Operations []operationSummary `json:"operations"`
			}{summaries}, err
		})
	addTool(server, "get_operation", "Read status, preflight, tasks, results, continuations and question history. In preflight, an action without ready_agent_ids is blocked; unavailable_agents explains candidate requirements, or is absent when no agent advertises the action. last_question is the latest pending question; remaining_questions counts other pending questions. Expired questions return to model review without an operator answer. An interrupted task has an unknown execution outcome; inspect before assigning replacement work.", operationSchema,
		func(ctx context.Context, request OperationRequest) (operationDetail, error) {
			operation, err := config.OperationStore.Get(ctx, request.OperationID)
			return describeOperation(operation), err
		})
	addTool(server, "get_report", "Read the separately stored final report for a completed operation, including after a server restart.", operationSchema,
		func(ctx context.Context, request OperationRequest) (reports.Report, error) {
			return readReport(ctx, config.OperationStore, config.ReportStore, request.OperationID)
		})
	addTool(server, "assign_task", "Queue an action task. Omit agent_id for automatic selection. Omit step_id unless using a step explicitly declared in the mission. Returns immediately; inspect operation events for completion. Action input must match its advertised schema.", objectSchema(map[string]any{
		"operation_id": stringProperty,
		"step_id":      missionStepProperty,
		"agent_id":     stringProperty,
		"action_name":  map[string]any{"type": "string", "minLength": 1, "description": "Action to execute, such as discover-pages. An action name is not a mission step ID."},
		"input":        map[string]any{"type": "object"},
	}, "operation_id", "action_name"),
		func(ctx context.Context, request AssignTaskRequest) (operations.Task, error) {
			return execution.Assign(ctx, commands.AssignTaskCommand(request))
		})
	addTool(server, "resolve_continuation", "Resolve a pending model continuation using its current version from get_operation. Supply the chosen actions, or an empty array to finish the branch. Records the decision and queues all child tasks atomically; repeated or stale decisions are rejected. Agent selection is optional. After an operator answer, review their guidance before resolving.", objectSchema(map[string]any{
		"operation_id": stringProperty, "task_id": stringProperty, "version": map[string]any{"type": "integer", "minimum": 1},
		"actions": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"action_name": stringProperty, "agent_id": stringProperty, "input": map[string]any{"type": "object"}, "reason": stringProperty}, "required": []string{"action_name"}}},
	}, "operation_id", "task_id", "version", "actions"), func(ctx context.Context, request ResolveContinuationRequest) (operations.Operation, error) {
		return execution.Resolve(ctx, commands.ResolveContinuationCommand(request))
	})
	addTool(server, "operation_events", "Read updates after a sequence cursor. Optionally wait up to 30 seconds. Inspect referenced tasks/questions with get_operation; save next_sequence for the next call.", objectSchema(map[string]any{"operation_id": stringProperty, "after_sequence": map[string]any{"type": "integer", "minimum": 0}, "wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 30}}, "operation_id"),
		func(ctx context.Context, request OperationEventsRequest) (OperationEventsResult, error) {
			waitCtx := ctx
			if request.WaitSeconds > 0 {
				var cancel context.CancelFunc
				waitCtx, cancel = context.WithTimeout(ctx, time.Duration(request.WaitSeconds)*time.Second)
				defer cancel()
			}
			events, err := readEvents(waitCtx, config.OperationStore, request.OperationID, request.AfterSequence, request.WaitSeconds > 0)
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				err = nil
			}
			if events == nil {
				events = []operations.Event{}
			}
			return OperationEventsResult{Events: events, NextSequence: request.AfterSequence + uint64(len(events))}, err
		})
	addTool(server, "ask_question", "Ask the operator when needed, with up to 4 short options or a free-text question. timeout_seconds defaults to 120; after expiry, review the situation and decide how to continue. Silence is not approval. Supply task_id and continuation version to pause that decision; otherwise step_id blocks that step or omission blocks new assignments. Do not invent an operator answer.", objectSchema(map[string]any{"task_id": stringProperty, "version": map[string]any{"type": "integer", "minimum": 1}, "operation_id": stringProperty, "step_id": missionStepProperty, "text": stringProperty, "options": map[string]any{"type": "array", "maxItems": 4, "items": stringProperty}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 86400}}, "operation_id", "text"),
		func(ctx context.Context, request AskQuestionRequest) (operations.Question, error) {
			return execution.Ask(ctx, commands.AskQuestionCommand(request))
		})
	addTool(server, "answer_question", "Record the operator's actual answer. A linked continuation returns to the model for a decision; this does not execute or approve any action automatically.", objectSchema(map[string]any{"operation_id": stringProperty, "question_id": stringProperty, "answer": stringProperty}, "operation_id", "question_id", "answer"),
		func(ctx context.Context, request AnswerQuestionRequest) (operations.Operation, error) {
			return execution.Answer(ctx, commands.AnswerQuestionCommand(request))
		})
	addTool(server, "complete_operation", "Save a final report and complete an operation, after active tasks and questions are resolved. Output must match output_schema. Returns report_id; read the saved report with get_report. Describe remaining limitations in the report.", objectSchema(map[string]any{"operation_id": stringProperty, "output": map[string]any{}}, "operation_id", "output"),
		func(ctx context.Context, request CompleteOperationRequest) (operations.Operation, error) {
			return execution.Complete(ctx, commands.CompleteOperationCommand(request))
		})
	addTool(server, "fail_operation", "End an operation that cannot continue, retaining collected results and the failure reason.", objectSchema(map[string]any{"operation_id": stringProperty, "reason": stringProperty}, "operation_id", "reason"),
		func(ctx context.Context, request FailOperationRequest) (operations.Operation, error) {
			return execution.Fail(ctx, commands.FailOperationCommand(request))
		})
	addTool(server, "cancel_operation", "Prevent new tasks. Already dispatched work may still execute, and its results are retained until task timeout.", operationSchema,
		func(ctx context.Context, request OperationRequest) (operations.Operation, error) {
			return execution.Cancel(ctx, commands.CancelOperationCommand{OperationID: request.OperationID})
		})
}

var missionStepProperty = map[string]any{
	"type":        "string",
	"minLength":   1,
	"description": "Optional ID from the mission's explicitly declared steps. Omit when the mission has no steps. Do not substitute an action name.",
}

func readReport(ctx context.Context, store operations.OperationStore, reportStore reports.ReportStore, operationID string) (reports.Report, error) {
	operation, err := store.Get(ctx, operationID)
	if err != nil {
		return reports.Report{}, err
	}
	if operation.Status != operations.Completed || operation.ReportID == "" {
		return reports.Report{}, fmt.Errorf("operation has no stored report: %w", reports.ErrUnknownReport)
	}
	report, err := reportStore.Get(ctx, operation.ReportID)
	if err != nil {
		return reports.Report{}, err
	}
	if report.OperationID != operation.ID || report.Mission != operation.Mission.Name {
		return reports.Report{}, errors.New("stored report does not belong to the operation")
	}
	return report, nil
}

// Long polling reads durable snapshots, so event history and updates do not
// depend on a controller connection or in-memory notification channel.
func readEvents(ctx context.Context, store operations.OperationStore, operationID string, after uint64, wait bool) ([]operations.Event, error) {
	for {
		operation, err := store.Get(ctx, operationID)
		if err != nil {
			return nil, err
		}
		if after > uint64(len(operation.Events)) {
			return nil, errors.New("event cursor exceeds the latest sequence")
		}
		events := operation.Events[after:]
		finished := operation.Status.Terminal()
		for _, task := range operation.Tasks {
			if task.Status == operations.TaskRunning {
				finished = false
				break
			}
		}
		if len(events) > 0 || !wait || finished {
			return events, nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

var stringProperty = map[string]any{"type": "string", "minLength": 1}
var operationSchema = objectSchema(map[string]any{"operation_id": stringProperty}, "operation_id")

func objectSchema(properties map[string]any, required ...string) string {
	data, err := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
	if err != nil {
		panic(err)
	}
	return string(data)
}
