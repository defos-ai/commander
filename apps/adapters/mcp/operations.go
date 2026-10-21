package missionsmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/queries"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type MissionRequest struct {
	MissionName string `json:"mission_name"`
}

func (r MissionRequest) Validate() error {
	if strings.TrimSpace(r.MissionName) == "" {
		return errors.New("mission_name must not be blank")
	}
	return nil
}

type missionListItem struct {
	Name      string `json:"name"`
	Objective string `json:"objective"`
}

type OperationRequest struct {
	OperationID string `json:"operation_id"`
}

type TaskRequest struct {
	OperationRequest
	TaskID string `json:"task_id"`
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
	OperationPageRequest
	WaitSeconds int `json:"wait_seconds,omitempty"`
}

func (r OperationEventsRequest) Validate() error {
	if err := r.OperationPageRequest.Validate(); err != nil {
		return err
	}
	if r.WaitSeconds < 0 || r.WaitSeconds > 30 {
		return errors.New("wait_seconds must be between 0 and 30")
	}
	return nil
}

type OperationEventsResult struct {
	Events     []operations.Event `json:"events"`
	NextCursor string             `json:"next_cursor"`
}

type OperationTasksResult struct {
	Tasks      []taskSummary `json:"tasks"`
	NextCursor string        `json:"next_cursor"`
}

func addOperationTools(server *mcp.Server, config Config) {
	execution := config.Operations
	query := queries.Operations{Store: config.OperationStore, Agents: config.AgentStore, Reports: config.ReportStore}
	addTool(server, "list_missions", "List mission names and objectives. Use describe_mission for the selected mission's actions, rules and schemas before starting it.", emptySchema,
		func(ctx context.Context, _ struct{}) (struct {
			Missions []missionListItem `json:"missions"`
		}, error) {
			items, err := config.MissionStore.List(ctx)
			summaries := make([]missionListItem, 0, len(items))
			for _, item := range items {
				summaries = append(summaries, missionListItem{Name: item.Name, Objective: item.Objective})
			}
			return struct {
				Missions []missionListItem `json:"missions"`
			}{summaries}, err
		})
	addTool(server, "describe_mission", "Read one mission's actions, rules, steps and input/output schemas. Valid proposals run automatically; rules group results, request model review or constrain placement. same-agent keeps its for_actions on one agent instance per operation; it does not serialize tasks. Does not execute work.", objectSchema(map[string]any{"mission_name": stringProperty}, "mission_name"),
		func(ctx context.Context, request MissionRequest) (missions.Mission, error) {
			return config.MissionStore.Get(ctx, request.MissionName)
		})
	addTool(server, "start_operation", "Start a tracked operation from a registered mission. Optionally supply operation_id as a canonical lowercase UUID to track it before the response arrives; otherwise one is generated. Existing IDs return a conflict; use get_operation to inspect them. Returns preflight coverage: planning when ready, waiting when requirements are missing. Tasks remain queued until all declared mission requirements are ready.", objectSchema(map[string]any{"operation_id": map[string]any{"type": "string", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"}, "mission_name": stringProperty, "input": map[string]any{}}, "mission_name", "input"),
		func(ctx context.Context, request StartOperationRequest) (operationResult, error) {
			return commandOperation(execution.Start(ctx, commands.StartOperationCommand(request)))
		})
	addTool(server, "list_operations", "List operation IDs, mission names, inputs, status, affinity and progress (task counts, finished/total tasks, pending model decisions/questions and settlement). Use get_operation for results and decisions, or get_report for a final report.", emptySchema,
		func(ctx context.Context, _ struct{}) (struct {
			Operations []operationSummary `json:"operations"`
		}, error) {
			items, err := query.List(ctx, false)
			summaries := make([]operationSummary, 0, len(items))
			for _, item := range items {
				summaries = append(summaries, summarizeOperation(item))
			}
			return struct {
				Operations []operationSummary `json:"operations"`
			}{summaries}, err
		})
	addTool(server, "get_operation", "Read status, progress, preflight, affinity, tasks, results, continuations and question history. progress.finished_tasks counts succeeded, failed and cancelled tasks; interrupted tasks remain unfinished. progress.settled requires terminal status and all tasks finished; keep polling until settled even after cancellation or failure. affinity derives the same-agent for_actions and bound agent_id from saved dispatches; agent_id is absent before the first dispatch. Command responses contain saved state; use this query for the derived affinity view. Queued tasks remain unassigned. A busy or unavailable bound agent causes waiting, never fallback or automatic replay; an affinity error requires inspection, not retry or rebinding. In preflight, an action without ready_agent_ids is blocked; unavailable_agents explains candidate requirements, or is absent when no agent advertises the action. last_question is the latest pending question; remaining_questions counts other pending questions. Expired questions return to model review without an operator answer. Running tasks may include agent_type and missing_requirements from matching live execution. Supply secret values only through Admin Secrets or the Playground masked form; task deadlines continue. An interrupted task has an unknown execution outcome; inspect before assigning replacement work.", operationSchema,
		func(ctx context.Context, request OperationRequest) (operationDetail, error) {
			operation, err := query.Get(ctx, request.OperationID)
			return describeOperation(operation), err
		})
	addTool(server, "operation_tasks", "Read task summaries in assignment order, up to limit (default 100, max 1000). Pass next_cursor as cursor for the next page; an empty page keeps the cursor unchanged. Use get_task for input, results and proposed next actions.", operationPageSchema(false),
		func(ctx context.Context, request OperationPageRequest) (OperationTasksResult, error) {
			tasks, err := query.ListTasks(ctx, request.OperationID, request.Cursor, request.pageLimit())
			summaries := make([]taskSummary, 0, len(tasks))
			nextCursor := request.Cursor
			for _, task := range tasks {
				summaries = append(summaries, summarizeTask(task))
				nextCursor = task.ID
			}
			return OperationTasksResult{Tasks: summaries, NextCursor: nextCursor}, err
		})
	addTool(server, "get_task", "Read one task's input, result, artifacts, error and full continuation. Requires both operation_id and task_id; use the continuation version when resolving its decision.", objectSchema(map[string]any{"operation_id": stringProperty, "task_id": stringProperty}, "operation_id", "task_id"),
		func(ctx context.Context, request TaskRequest) (taskDetail, error) {
			task, err := query.GetTask(ctx, request.OperationID, request.TaskID)
			return describeTask(task), err
		})
	addTool(server, "get_report", "Read a completed operation's report by operation_id. Use list_operations to find the ID; no operator question is needed.", operationSchema,
		func(ctx context.Context, request OperationRequest) (reports.Report, error) {
			return query.GetReport(ctx, request.OperationID)
		})
	addTool(server, "assign_task", "Queue an action task; the controller selects a ready agent with available capacity and enforces same-agent placement. Clients do not select or rebind agents. Omit step_id unless using a step explicitly declared in the mission. Returns immediately; inspect operation events for completion. Action input must match its advertised schema.", objectSchema(map[string]any{
		"operation_id": stringProperty,
		"step_id":      missionStepProperty,
		"action_name":  map[string]any{"type": "string", "minLength": 1, "description": "Action to execute, such as discover-pages. An action name is not a mission step ID."},
		"input":        map[string]any{"type": "object"},
	}, "operation_id", "action_name"),
		func(ctx context.Context, request AssignTaskRequest) (operations.Task, error) {
			return execution.Assign(ctx, commands.AssignTaskCommand(request))
		})
	addTool(server, "resolve_continuation", "Resolve a pending model continuation using its current version from get_task or get_operation. Supply the chosen actions, or an empty array to finish the branch. Records the decision and queues all child tasks atomically; repeated or stale decisions are rejected. The controller selects agents. After an operator answer, review their guidance before resolving.", objectSchema(map[string]any{
		"operation_id": stringProperty, "task_id": stringProperty, "version": map[string]any{"type": "integer", "minimum": 1},
		"actions": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"action_name": stringProperty, "input": map[string]any{"type": "object"}, "reason": stringProperty}, "required": []string{"action_name"}}},
	}, "operation_id", "task_id", "version", "actions"), func(ctx context.Context, request ResolveContinuationRequest) (operationResult, error) {
		return commandOperation(execution.Resolve(ctx, commands.ResolveContinuationCommand(request)))
	})
	addTool(server, "operation_events", "Read events in sequence order, up to limit (default 100, max 1000). Pass next_cursor as cursor for the next page; an empty page keeps the cursor unchanged. Optionally wait up to 30 seconds. Inspect tasks with get_task and questions with get_operation.", operationPageSchema(true),
		func(ctx context.Context, request OperationEventsRequest) (OperationEventsResult, error) {
			var after uint64
			if request.Cursor != "" {
				var err error
				after, err = strconv.ParseUint(request.Cursor, 10, 64)
				if err != nil {
					return OperationEventsResult{}, errors.New("invalid event cursor")
				}
			}
			waitCtx := ctx
			if request.WaitSeconds > 0 {
				var cancel context.CancelFunc
				waitCtx, cancel = context.WithTimeout(ctx, time.Duration(request.WaitSeconds)*time.Second)
				defer cancel()
			}
			events, err := query.ListEvents(waitCtx, request.OperationID, after, request.pageLimit(), request.WaitSeconds > 0)
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				err = nil
			}
			if events == nil {
				events = []operations.Event{}
			}
			nextCursor := request.Cursor
			if len(events) > 0 {
				nextCursor = strconv.FormatUint(events[len(events)-1].Sequence, 10)
			}
			return OperationEventsResult{Events: events, NextCursor: nextCursor}, err
		})
	addTool(server, "ask_question", "Ask an operator decision for an active operation (up to 4 options or free text). General clarifications belong in chat. Supply task_id/version to pause a continuation; otherwise step_id blocks that step, or omit it to block new assignments. Timeout defaults to 120 seconds; expiry returns to model review, not approval.", objectSchema(map[string]any{"task_id": stringProperty, "version": map[string]any{"type": "integer", "minimum": 1}, "operation_id": stringProperty, "step_id": missionStepProperty, "text": stringProperty, "options": map[string]any{"type": "array", "maxItems": 4, "items": stringProperty}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 86400}}, "operation_id", "text"),
		func(ctx context.Context, request AskQuestionRequest) (operations.Question, error) {
			question, err := execution.Ask(ctx, commands.AskQuestionCommand(request))
			if errors.Is(err, operations.ErrFinished) {
				return question, fmt.Errorf("%w: ask clarifications in chat; read saved results or start a new operation for new work", err)
			}
			return question, err
		})
	addTool(server, "answer_question", "Record the operator's actual answer. A linked continuation returns to the model for a decision; this does not execute or approve any action automatically.", objectSchema(map[string]any{"operation_id": stringProperty, "question_id": stringProperty, "answer": stringProperty}, "operation_id", "question_id", "answer"),
		func(ctx context.Context, request AnswerQuestionRequest) (operationResult, error) {
			return commandOperation(execution.Answer(ctx, commands.AnswerQuestionCommand(request)))
		})
	addTool(server, "complete_operation", "Save a final report and complete an operation, after active tasks and questions are resolved. Output must match output_schema. Returns report_id; read the saved report with get_report. Describe remaining limitations in the report.", objectSchema(map[string]any{"operation_id": stringProperty, "output": map[string]any{}}, "operation_id", "output"),
		func(ctx context.Context, request CompleteOperationRequest) (operationResult, error) {
			return commandOperation(execution.Complete(ctx, commands.CompleteOperationCommand(request)))
		})
	addTool(server, "fail_operation", "End unrecoverable work, retaining results and the reason. Consider recovery or an operator decision first.", objectSchema(map[string]any{"operation_id": stringProperty, "reason": stringProperty}, "operation_id", "reason"),
		func(ctx context.Context, request FailOperationRequest) (operationResult, error) {
			return commandOperation(execution.Fail(ctx, commands.FailOperationCommand(request)))
		})
	addTool(server, "cancel_operation", "Prevent new tasks. Already dispatched work may still execute, and its results are retained until task timeout.", operationSchema,
		func(ctx context.Context, request OperationRequest) (operationResult, error) {
			return commandOperation(execution.Cancel(ctx, commands.CancelOperationCommand{OperationID: request.OperationID}))
		})
}

var missionStepProperty = map[string]any{
	"type":        "string",
	"minLength":   1,
	"description": "Optional ID from the mission's explicitly declared steps. Omit when the mission has no steps. Do not substitute an action name.",
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
