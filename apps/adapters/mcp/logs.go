package missionsmcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/queries"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type OperationLogsRequest struct {
	OperationPageRequest
	TaskID     logFilter `json:"task_id,omitempty"`
	AgentID    logFilter `json:"agent_id,omitempty"`
	ActionName logFilter `json:"action_name,omitempty"`
	Order      logFilter `json:"order,omitempty"`
}

type logFilter string

func (f *logFilter) UnmarshalJSON(data []byte) error {
	var value *string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == nil || *value == "" {
		return errors.New("log filters must be nonempty strings")
	}
	*f = logFilter(*value)
	return nil
}

func (r OperationLogsRequest) Validate() error {
	if !id.Valid(r.OperationID) {
		return errors.New("operation_id must be a canonical UUID")
	}
	return r.query().Validate()
}

func (r OperationLogsRequest) query() executions.LogQuery {
	return executions.LogQuery{OperationID: r.OperationID, TaskID: string(r.TaskID), AgentID: string(r.AgentID),
		ActionName: string(r.ActionName), AfterLogID: r.Cursor, Limit: r.pageLimit(), Order: executions.LogOrder(r.Order)}
}

type executionLog struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	CommandID   string    `json:"command_id"`
	ActionName  string    `json:"action_name"`
	OperationID string    `json:"operation_id"`
	TaskID      string    `json:"task_id"`
	StepID      string    `json:"step_id,omitempty"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at"`
}

type OperationLogsResult struct {
	Logs       []executionLog `json:"logs"`
	NextCursor string         `json:"next_cursor"`
}

func addLogTools(server *mcp.Server, query queries.Operations) {
	addTool(server, "operation_logs", "Read saved execution progress logs for one operation, optionally filtered by task_id and exact agent_id/action_name. Order asc (default) reads oldest first; desc reads newest first. Limit defaults to 100, maximum 1000. Page with next_cursor using the same scope, filters and order; an empty page keeps it unchanged. Descending next_cursor points toward older entries. To follow new logs, switch to asc using the FIRST/newest descending entry ID as cursor. Logs are observations, not checkpoints or terminal results; delayed logs can arrive after completion. No running controller is required.", logQuerySchema(),
		func(ctx context.Context, request OperationLogsRequest) (OperationLogsResult, error) {
			return listExecutionLogs(ctx, query, request.query(), request.Cursor)
		})
}

func listExecutionLogs(ctx context.Context, query queries.Operations, criteria executions.LogQuery, cursor string) (OperationLogsResult, error) {
	result := OperationLogsResult{Logs: []executionLog{}, NextCursor: cursor}
	logs, err := query.ListLogs(ctx, criteria)
	if err != nil {
		return result, err
	}
	for _, log := range logs {
		result.Logs = append(result.Logs, executionLog{ID: log.ID, AgentID: log.AgentID,
			CommandID: log.CommandID, ActionName: log.ActionName, OperationID: log.Work.OperationID,
			TaskID: log.Work.TaskID, StepID: log.Work.StepID, Message: log.Message, CreatedAt: log.CreatedAt})
		result.NextCursor = log.ID
	}
	return result, nil
}

func logQuerySchema() string {
	properties := map[string]any{
		"operation_id": map[string]any{"type": "string", "format": "uuid", "minLength": 1},
		"task_id":      map[string]any{"type": "string", "format": "uuid", "minLength": 1},
		"agent_id":     stringProperty, "action_name": map[string]any{"type": "string", "pattern": `\S`},
		"cursor": map[string]any{"type": []string{"string", "null"}, "description": "Copy next_cursor with the same scope, filters and order. Omit, pass null or an empty string to start. For live follow, use asc with the first/newest descending entry ID."},
		"limit": map[string]any{"type": []string{"integer", "null"}, "minimum": 1, "maximum": domain.MaxPageSize,
			"default": defaultPageLimit, "description": "Omit or pass null for the default limit of 100."},
		"order": map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "default": "asc",
			"description": "Append order: asc is oldest first; desc is newest first. Descending cursors continue toward older entries."},
	}
	return objectSchema(properties, "operation_id")
}
