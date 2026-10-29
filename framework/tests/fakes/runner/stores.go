package fakes

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
)

func NewOperationStore() *OperationStore {
	return &OperationStore{records: make(map[string]operations.Operation)}
}

type OperationStore struct {
	mu       sync.Mutex
	records  map[string]operations.Operation
	failNext error
}

func (s *OperationStore) FailNextUpdate(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = err
}

func (s *OperationStore) Create(_ context.Context, op operations.Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[op.ID]; exists {
		return operations.ErrOperationConflict
	}
	s.records[op.ID] = op.Clone()
	return nil
}
func (s *OperationStore) Get(_ context.Context, id string) (operations.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, exists := s.records[id]
	if !exists {
		return operations.Operation{}, operations.ErrUnknownOperation
	}
	return op.Clone(), nil
}
func (s *OperationStore) List(context.Context) ([]operations.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []operations.Operation
	for _, op := range s.records {
		result = append(result, op.Clone())
	}
	return result, nil
}
func (s *OperationStore) ListEvents(ctx context.Context, id string, after uint64, limit int) ([]operations.Event, error) {
	if err := domain.ValidatePageLimit(limit); err != nil {
		return nil, err
	}
	op, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	events := []operations.Event{}
	for _, event := range op.Events {
		if event.Sequence > after {
			events = append(events, event)
			if len(events) == limit {
				break
			}
		}
	}
	return events, nil
}
func (s *OperationStore) GetTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
	op, err := s.Get(ctx, operationID)
	if err != nil {
		return operations.Task{}, err
	}
	for _, task := range op.Tasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return operations.Task{}, operations.ErrUnknownTask
}
func (s *OperationStore) ListTasks(ctx context.Context, operationID, afterTaskID string, limit int) ([]operations.Task, error) {
	if err := domain.ValidatePageLimit(limit); err != nil {
		return nil, err
	}
	op, err := s.Get(ctx, operationID)
	if err != nil {
		return nil, err
	}
	start := 0
	if afterTaskID != "" {
		index := slices.IndexFunc(op.Tasks, func(task operations.Task) bool { return task.ID == afterTaskID })
		if index < 0 {
			return nil, operations.ErrUnknownTask
		}
		start = index + 1
	}
	return op.Tasks[start : start+min(limit, len(op.Tasks)-start)], nil
}
func (s *OperationStore) Update(ctx context.Context, id string, mutate func(*operations.Operation) error) (operations.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return operations.Operation{}, err
	}
	if s.failNext != nil {
		err := s.failNext
		s.failNext = nil
		return operations.Operation{}, err
	}
	previous, ok := s.records[id]
	if !ok {
		return operations.Operation{}, operations.ErrUnknownOperation
	}
	next := previous.Clone()
	if err := mutate(&next); err != nil {
		return operations.Operation{}, err
	}
	s.records[id] = next.Clone()
	return next.Clone(), nil
}

func (s *OperationStore) DispatchTask(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		return op.DispatchTask(dispatch)
	})
}

func (s *OperationStore) QueueTask(ctx context.Context, operationID string, task operations.Task) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		_, err := op.QueueTask(task)
		return err
	})
}

func (s *OperationStore) FinishTask(ctx context.Context, operationID string, result operations.TaskResult) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		_, err := op.FinishTask(result)
		return err
	})
}

func (s *OperationStore) ResolveContinuation(ctx context.Context, operationID, taskID string, version uint64, next []actions.NextAction) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		return op.ResolveContinuation(taskID, version, next)
	})
}

func (s *OperationStore) AskQuestion(ctx context.Context, operationID string, question operations.Question, version uint64) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		return op.AskQuestion(question, version)
	})
}

func (s *OperationStore) AnswerQuestion(ctx context.Context, operationID, questionID, answer string, answeredBy operations.AnswerOrigin) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		return op.AnswerQuestion(questionID, answer, answeredBy)
	})
}

func (s *OperationStore) RefreshPreflight(ctx context.Context, operationID string, records []agents.Agent) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		_, err := op.RefreshPreflight(records)
		return err
	})
}

func (s *OperationStore) Recover(ctx context.Context, operationID string, now time.Time) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		_, err := op.Recover(now)
		return err
	})
}

func (s *OperationStore) Cancel(ctx context.Context, operationID string) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		return op.Cancel()
	})
}

func (s *OperationStore) Complete(ctx context.Context, operationID, reportID string, output json.RawMessage, completedBy operations.CompletionOrigin) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		return op.Complete(reportID, output, completedBy)
	})
}

func (s *OperationStore) Fail(ctx context.Context, operationID, reason string) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(op *operations.Operation) error {
		return op.Fail(reason)
	})
}

type MissionStore struct {
	mission atomic.Pointer[missions.Mission]
}

func NewMissionStore(mission missions.Mission) *MissionStore {
	s := &MissionStore{}
	s.Set(mission)
	return s
}
func (*MissionStore) Create(context.Context, missions.Mission) error {
	return errors.New("runner must not create missions")
}
func (s *MissionStore) Set(mission missions.Mission) { copy := mission.Clone(); s.mission.Store(&copy) }
func (s *MissionStore) Change(change func(*missions.Mission)) {
	snapshot := s.Snapshot()
	change(&snapshot)
	s.Set(snapshot)
}
func (s *MissionStore) Snapshot() missions.Mission {
	if m := s.mission.Load(); m != nil {
		return m.Clone()
	}
	return missions.Mission{}
}
func (s *MissionStore) Upsert(_ context.Context, mission missions.Mission) error {
	s.Set(mission)
	return nil
}
func (s *MissionStore) Get(context.Context, string) (missions.Mission, error) {
	return s.Snapshot(), nil
}
func (s *MissionStore) List(context.Context) ([]missions.Mission, error) {
	return []missions.Mission{s.Snapshot()}, nil
}

type ReportStore struct{}

func (ReportStore) Put(context.Context, reports.Report) error { return nil }
func (ReportStore) Get(context.Context, string) (reports.Report, error) {
	return reports.Report{}, reports.ErrUnknownReport
}

type AgentCatalog struct {
	records atomic.Value
}

func (c *AgentCatalog) Agents(context.Context) ([]agents.Agent, error) {
	records, _ := c.records.Load().([]agents.Agent)
	result := make([]agents.Agent, len(records))
	for i, record := range records {
		result[i] = record.Clone()
	}
	return result, nil
}

func (c *AgentCatalog) Set(records ...agents.Agent) {
	snapshot := make([]agents.Agent, len(records))
	for i, record := range records {
		snapshot[i] = record.Clone()
	}
	c.records.Store(snapshot)
}

type AgentSelectorFunc func(context.Context, runner.AgentSelection) (string, error)

func (AgentSelectorFunc) Name() string { return "test-selector" }
func (f AgentSelectorFunc) Select(ctx context.Context, selection runner.AgentSelection) (string, error) {
	return f(ctx, selection)
}

type RuleSelectorFunc func(context.Context, runner.RuleSelection) (missions.RuleSet, error)

func (RuleSelectorFunc) Name() string { return "test-rules" }
func (f RuleSelectorFunc) Select(ctx context.Context, selection runner.RuleSelection) (missions.RuleSet, error) {
	return f(ctx, selection)
}
