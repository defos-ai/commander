package fakes

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
)

type UncertainWriteStore struct {
	operations.OperationStore
	CreateError    error
	Dispatch       func(context.Context, string, operations.TaskDispatch) (operations.Operation, error)
	AfterQueue     func(operations.Operation) error
	AfterDispatch  func(operations.Operation) error
	AfterFinish    func(operations.Operation) error
	AfterResolve   func(operations.Operation) error
	AfterAsk       func(operations.Operation) error
	AfterAnswer    func(operations.Operation) error
	AfterPreflight func(operations.Operation) error
	AfterRecover   func(operations.Operation) error
	AfterCancel    func(operations.Operation) error
	AfterComplete  func(operations.Operation) error
	AfterFail      func(operations.Operation) error
}

func (s *UncertainWriteStore) Create(ctx context.Context, op operations.Operation) error {
	if err := s.OperationStore.Create(ctx, op); err != nil {
		return err
	}
	return s.CreateError
}

func (s *UncertainWriteStore) QueueTask(ctx context.Context, operationID string, task operations.Task) (operations.Operation, error) {
	op, err := s.OperationStore.QueueTask(ctx, operationID, task)
	if err == nil && s.AfterQueue != nil {
		err = s.AfterQueue(op)
	}
	return op, err
}

func (s *UncertainWriteStore) DispatchTask(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
	if s.Dispatch != nil {
		return s.Dispatch(ctx, operationID, dispatch)
	}
	op, err := s.OperationStore.DispatchTask(ctx, operationID, dispatch)
	if err == nil && s.AfterDispatch != nil {
		err = s.AfterDispatch(op)
	}
	return op, err
}

func (s *UncertainWriteStore) FinishTask(ctx context.Context, operationID string, result operations.TaskResult) (operations.Operation, error) {
	op, err := s.OperationStore.FinishTask(ctx, operationID, result)
	if err == nil && s.AfterFinish != nil {
		err = s.AfterFinish(op)
	}
	return op, err
}

func (s *UncertainWriteStore) ResolveContinuation(ctx context.Context, operationID, taskID string, version uint64, next []actions.NextAction) (operations.Operation, error) {
	op, err := s.OperationStore.ResolveContinuation(ctx, operationID, taskID, version, next)
	if err == nil && s.AfterResolve != nil {
		err = s.AfterResolve(op)
	}
	return op, err
}

func (s *UncertainWriteStore) AskQuestion(ctx context.Context, operationID string, question operations.Question, version uint64) (operations.Operation, error) {
	op, err := s.OperationStore.AskQuestion(ctx, operationID, question, version)
	if err == nil && s.AfterAsk != nil {
		err = s.AfterAsk(op)
	}
	return op, err
}

func (s *UncertainWriteStore) AnswerQuestion(ctx context.Context, operationID, questionID, answer string, answeredBy operations.AnswerOrigin) (operations.Operation, error) {
	op, err := s.OperationStore.AnswerQuestion(ctx, operationID, questionID, answer, answeredBy)
	if err == nil && s.AfterAnswer != nil {
		err = s.AfterAnswer(op)
	}
	return op, err
}

func (s *UncertainWriteStore) RefreshPreflight(ctx context.Context, operationID string, records []agents.Agent) (operations.Operation, error) {
	op, err := s.OperationStore.RefreshPreflight(ctx, operationID, records)
	if err == nil && s.AfterPreflight != nil {
		err = s.AfterPreflight(op)
	}
	return op, err
}

func (s *UncertainWriteStore) Recover(ctx context.Context, operationID string, now time.Time) (operations.Operation, error) {
	op, err := s.OperationStore.Recover(ctx, operationID, now)
	if err == nil && s.AfterRecover != nil {
		err = s.AfterRecover(op)
	}
	return op, err
}

func (s *UncertainWriteStore) Cancel(ctx context.Context, operationID string) (operations.Operation, error) {
	op, err := s.OperationStore.Cancel(ctx, operationID)
	if err == nil && s.AfterCancel != nil {
		err = s.AfterCancel(op)
	}
	return op, err
}

func (s *UncertainWriteStore) Complete(ctx context.Context, operationID, reportID string, output json.RawMessage) (operations.Operation, error) {
	op, err := s.OperationStore.Complete(ctx, operationID, reportID, output)
	if err == nil && s.AfterComplete != nil {
		err = s.AfterComplete(op)
	}
	return op, err
}

func (s *UncertainWriteStore) Fail(ctx context.Context, operationID, reason string) (operations.Operation, error) {
	op, err := s.OperationStore.Fail(ctx, operationID, reason)
	if err == nil && s.AfterFail != nil {
		err = s.AfterFail(op)
	}
	return op, err
}

type DurableExecutor struct{ Executor }

func (e DurableExecutor) DispatchAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return e.RunAction(ctx, agentID, call)
}

type TerminalReportStore struct {
	records  map[string]reports.Report
	failNext error
	afterPut func(context.Context, reports.Report) error
}

func (s *TerminalReportStore) Put(ctx context.Context, report reports.Report) error {
	if s.failNext != nil {
		err := s.failNext
		s.failNext = nil
		return err
	}
	s.records[report.ID] = report.Clone()
	if s.afterPut != nil {
		return s.afterPut(ctx, report)
	}
	return nil
}

func (s *TerminalReportStore) Get(_ context.Context, id string) (reports.Report, error) {
	report, exists := s.records[id]
	if !exists {
		return reports.Report{}, reports.ErrUnknownReport
	}
	return report.Clone(), nil
}

func (s *TerminalReportStore) Count() int {
	return len(s.records)
}

type AfterListStore struct {
	operations.OperationStore
	AfterList func() error
}

func (s *AfterListStore) List(ctx context.Context) ([]operations.Operation, error) {
	ops, err := s.OperationStore.List(ctx)
	if err == nil && s.AfterList != nil {
		err = s.AfterList()
		s.AfterList = nil
	}
	return ops, err
}

type RecoveryFailureStore struct {
	operations.OperationStore
	Failure error
}

func (s *RecoveryFailureStore) Recover(context.Context, string, time.Time) (operations.Operation, error) {
	return operations.Operation{}, s.Failure
}

type PreflightFailureStore struct {
	operations.OperationStore
	allowed   atomic.Bool
	attempted chan struct{}
	Failure   error
}

func (s *PreflightFailureStore) RefreshPreflight(ctx context.Context, operationID string, records []agents.Agent) (operations.Operation, error) {
	if records[0].Actions[0].Status == actions.Ready && !s.allowed.Load() {
		select {
		case s.attempted <- struct{}{}:
		default:
		}
		return operations.Operation{}, s.Failure
	}
	return s.OperationStore.RefreshPreflight(ctx, operationID, records)
}

type DispatchFailureStore struct {
	operations.OperationStore
	allowed   atomic.Bool
	attempted chan struct{}
}

func (s *DispatchFailureStore) DispatchTask(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
	if !s.allowed.Load() {
		select {
		case s.attempted <- struct{}{}:
		default:
		}
		return operations.Operation{}, errors.New("database unavailable")
	}
	return s.OperationStore.DispatchTask(ctx, operationID, dispatch)
}

type DeadlineOnAnswerStore struct{ *OperationStore }

func (s *DeadlineOnAnswerStore) AnswerQuestion(ctx context.Context, operationID, questionID, answer string, answeredBy operations.AnswerOrigin) (operations.Operation, error) {
	if _, err := s.OperationStore.Update(ctx, operationID, func(op *operations.Operation) error {
		for i := range op.Questions {
			if op.Questions[i].ID == questionID && op.Questions[i].Pending() {
				op.Questions[i].AskedAt = time.Now().Add(-2 * time.Second)
				op.Questions[i].FallbackAt = time.Now().Add(-time.Second)
			}
		}
		return nil
	}); err != nil {
		return operations.Operation{}, err
	}
	return s.OperationStore.AnswerQuestion(ctx, operationID, questionID, answer, answeredBy)
}

// A store snapshot may contain another assignment committed after the requested task.
type AdditionalQueuedTaskStore struct{ operations.OperationStore }

func (s *AdditionalQueuedTaskStore) QueueTask(ctx context.Context, operationID string, task operations.Task) (operations.Operation, error) {
	op, err := s.OperationStore.QueueTask(ctx, operationID, task)
	if err != nil {
		return op, err
	}
	later := operations.NewTask(operationID, "", actions.NextAction{ActionName: "follow-up"})
	return s.OperationStore.QueueTask(ctx, operationID, later)
}

type SelectionCatalog struct{ records []agents.Agent }

func (c *SelectionCatalog) Agents(context.Context) ([]agents.Agent, error) { return c.records, nil }

func NewTerminalReportStore() *TerminalReportStore {
	return &TerminalReportStore{records: make(map[string]reports.Report)}
}
func (s *TerminalReportStore) FailNext(err error) { s.failNext = err }
func (s *TerminalReportStore) AfterPut(callback func(context.Context, reports.Report) error) {
	s.afterPut = callback
}
func NewSelectionCatalog(records ...agents.Agent) *SelectionCatalog {
	snapshot := make([]agents.Agent, len(records))
	for i, record := range records {
		snapshot[i] = record.Clone()
	}
	return &SelectionCatalog{records: snapshot}
}
func (c *SelectionCatalog) Snapshot() []agents.Agent {
	snapshot := make([]agents.Agent, len(c.records))
	for i, record := range c.records {
		snapshot[i] = record.Clone()
	}
	return snapshot
}
func (s *DispatchFailureStore) Allow()                     { s.allowed.Store(true) }
func (s *DispatchFailureStore) Attempted() <-chan struct{} { return s.attempted }
func NewDispatchFailureStore(store operations.OperationStore) *DispatchFailureStore {
	return &DispatchFailureStore{OperationStore: store, attempted: make(chan struct{}, 1)}
}
func (s *PreflightFailureStore) Allow() { s.allowed.Store(true) }
func NewPreflightFailureStore(store operations.OperationStore, failure error, attempted chan struct{}) *PreflightFailureStore {
	return &PreflightFailureStore{OperationStore: store, attempted: attempted, Failure: failure}
}
