package queries

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
)

// Operations reads persisted state without requiring a controller connection.
// Agents is optional: inventory failures omit live observations, while caller
// cancellation still ends the query. Reports and Executions are needed only by
// GetReport and ListLogs respectively.
type Operations struct {
	Store      operations.OperationStore
	Agents     agents.AgentStore
	Reports    reports.ReportStore
	Executions executions.ExecutionStore
}

// Views keep observations separate from saved entities. Adapters own their wire
// formats; these values are neither persisted nor serialized directly.
type OperationView struct {
	operations.Operation
	Tasks    []TaskView
	Progress operations.Progress
	Affinity *operations.Affinity
}

type TaskView struct {
	operations.Task
	AgentType           string
	MissingRequirements []actions.Requirement
}

// List preserves store ordering. includeRequirements enables one shared inventory
// read for detail listings; compact summaries need only saved state.
func (q Operations) List(ctx context.Context, includeRequirements bool) ([]OperationView, error) {
	items, err := q.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	var records map[string]agents.Agent
	if includeRequirements && slices.ContainsFunc(items, func(item operations.Operation) bool { return !item.Status.Terminal() }) {
		records, err = q.readAgents(ctx)
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	views := make([]OperationView, len(items))
	for i, item := range items {
		views[i] = operationView(item, records, now)
	}
	return views, ctx.Err()
}

func (q Operations) Get(ctx context.Context, operationID string) (OperationView, error) {
	item, err := q.Store.Get(ctx, operationID)
	if err != nil {
		return OperationView{}, err
	}
	var records map[string]agents.Agent
	if !item.Status.Terminal() {
		records, err = q.readAgents(ctx)
		if err != nil {
			return OperationView{}, err
		}
	}
	return operationView(item, records, time.Now().UTC()), ctx.Err()
}

func (q Operations) ListTasks(ctx context.Context, operationID, afterTaskID string, limit int) ([]TaskView, error) {
	items, err := q.Store.ListTasks(ctx, operationID, afterTaskID, limit)
	if err != nil {
		return nil, err
	}
	var records map[string]agents.Agent
	if len(items) != 0 {
		records, err = q.readOperationAgents(ctx, operationID)
		if err != nil {
			return nil, err
		}
	}
	return taskViews(items, operationID, records, time.Now().UTC()), ctx.Err()
}

func (q Operations) GetTask(ctx context.Context, operationID, taskID string) (TaskView, error) {
	item, err := q.Store.GetTask(ctx, operationID, taskID)
	if err != nil {
		return TaskView{}, err
	}
	var records map[string]agents.Agent
	if item.OperationID == operationID {
		records, err = q.readOperationAgents(ctx, operationID)
		if err != nil {
			return TaskView{}, err
		}
	}
	return taskView(item, records[item.AgentID], time.Now().UTC()), ctx.Err()
}

func (q Operations) GetReport(ctx context.Context, operationID string) (reports.Report, error) {
	operation, err := q.Store.Get(ctx, operationID)
	if err != nil {
		return reports.Report{}, err
	}
	if operation.Status != operations.Completed || operation.ReportID == "" {
		return reports.Report{}, fmt.Errorf("operation has no stored report: %w", reports.ErrUnknownReport)
	}
	report, err := q.Reports.Get(ctx, operation.ReportID)
	if err != nil {
		return reports.Report{}, err
	}
	if report.OperationID != operation.ID || report.Mission != operation.Mission.Name {
		return reports.Report{}, errors.New("stored report does not belong to the operation")
	}
	return report, ctx.Err()
}

// ListEvents can wait for durable events without a controller connection. The
// caller supplies the wait deadline; a terminal status alone does not end a wait
// while saved tasks still have unknown outcomes.
func (q Operations) ListEvents(ctx context.Context, operationID string, after uint64, limit int, wait bool) ([]operations.Event, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		events, err := q.Store.ListEvents(ctx, operationID, after, limit)
		if err != nil || len(events) > 0 || !wait {
			return events, err
		}
		operation, err := q.Store.Get(ctx, operationID)
		if err != nil {
			return nil, err
		}
		if operation.AssessProgress(time.Now().UTC()).Settled {
			// Settlement may have committed after the first event read.
			return q.Store.ListEvents(ctx, operationID, after, limit)
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

func (q Operations) readAgents(ctx context.Context) (map[string]agents.Agent, error) {
	if q.Agents == nil {
		return nil, ctx.Err()
	}
	records, err := q.Agents.List(ctx)
	if err != nil {
		return nil, ctx.Err()
	}
	byID := make(map[string]agents.Agent, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	return byID, ctx.Err()
}

func (q Operations) readOperationAgents(ctx context.Context, operationID string) (map[string]agents.Agent, error) {
	operation, err := q.Store.Get(ctx, operationID)
	if err != nil {
		// The task was already loaded. If its parent's current status cannot be
		// observed, return saved work without advertising a live wait.
		return nil, ctx.Err()
	}
	if operation.ID != operationID || operation.Status.Terminal() {
		return nil, ctx.Err()
	}
	return q.readAgents(ctx)
}

func operationView(item operations.Operation, records map[string]agents.Agent, now time.Time) OperationView {
	if item.Status.Terminal() {
		records = nil
	}
	return OperationView{
		Operation: item,
		Tasks:     taskViews(item.Tasks, item.ID, records, now),
		Progress:  item.AssessProgress(now),
		Affinity:  item.AssessAffinity(),
	}
}

func taskViews(items []operations.Task, operationID string, records map[string]agents.Agent, now time.Time) []TaskView {
	if items == nil {
		return nil
	}
	views := make([]TaskView, len(items))
	for i, item := range items {
		var record agents.Agent
		if item.OperationID == operationID {
			record = records[item.AgentID]
		}
		views[i] = taskView(item, record, now)
	}
	return views
}

func taskView(item operations.Task, record agents.Agent, now time.Time) TaskView {
	view := TaskView{Task: item}
	if requirements := operations.AssessTaskRequirements(item, record, now); len(requirements) != 0 {
		view.AgentType = string(record.Type)
		view.MissingRequirements = requirements
	}
	return view
}
