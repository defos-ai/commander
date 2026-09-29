package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"commander/framework/artifact"
	"commander/framework/comms/clients/controller/commands"
	"commander/framework/controller/runner"
	"commander/framework/domain/actions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
)

const agentID = "review-agent-00000001"

type operationStore struct {
	mu       sync.Mutex
	records  map[string]operations.Operation
	failNext error
}

func (s *operationStore) failNextUpdate(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = err
}

func (s *operationStore) Create(_ context.Context, op operations.Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[op.ID]; exists {
		return operations.ErrOperationConflict
	}
	s.records[op.ID] = op.Clone()
	return nil
}
func (s *operationStore) Get(_ context.Context, id string) (operations.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, exists := s.records[id]
	if !exists {
		return operations.Operation{}, operations.ErrUnknownOperation
	}
	return op.Clone(), nil
}
func (s *operationStore) List(context.Context) ([]operations.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []operations.Operation
	for _, op := range s.records {
		result = append(result, op.Clone())
	}
	return result, nil
}
func (s *operationStore) ListEvents(ctx context.Context, id string, after uint64, limit int) ([]operations.Event, error) {
	if limit <= 0 {
		return nil, errors.New("event limit must be positive")
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
func (s *operationStore) GetTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
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
func (s *operationStore) ListTasks(ctx context.Context, operationID, afterTaskID string, limit int) ([]operations.Task, error) {
	if limit <= 0 {
		return nil, errors.New("task limit must be positive")
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
func (s *operationStore) Update(ctx context.Context, id string, mutate func(*operations.Operation) error) (operations.Operation, error) {
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

type missionStore struct{ mission missions.Mission }

func (s *missionStore) Upsert(_ context.Context, m missions.Mission) error {
	s.mission = m.Clone()
	return nil
}
func (s *missionStore) Get(context.Context, string) (missions.Mission, error) {
	return s.mission.Clone(), nil
}
func (s *missionStore) List(context.Context) ([]missions.Mission, error) {
	return []missions.Mission{s.mission.Clone()}, nil
}

type reportStore struct{}

func (reportStore) Put(context.Context, reports.Report) error { return nil }
func (reportStore) Get(context.Context, string) (reports.Report, error) {
	return reports.Report{}, reports.ErrUnknownReport
}

type artifactRestorer struct{}

func (artifactRestorer) RestoreArtifacts([]artifact.Ref) error { return nil }

type executor struct {
	run     func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error)
	recover func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error)
}

func (e executor) RunAction(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
	if e.run == nil {
		return actions.ActionOutput{}, errors.New("unexpected new dispatch")
	}
	return e.run(ctx, agent, call)
}
func (e executor) RecoverAction(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
	if e.recover == nil {
		return actions.ActionOutput{}, errors.New("unexpected recovery")
	}
	return e.recover(ctx, agent, call)
}

type invocation struct {
	agent string
	call  actions.ActionCall
	reply chan actions.ActionOutput
}

func controlledExecutor() (executor, <-chan invocation) {
	calls := make(chan invocation, 16)
	return executor{run: func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		job := invocation{agent: agent, call: call, reply: make(chan actions.ActionOutput, 1)}
		calls <- job
		select {
		case result := <-job.reply:
			return result, nil
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
	}}, calls
}

func fixture() runner.Config {
	return runner.Config{
		MissionStore:   &missionStore{mission: missions.Mission{Name: "review", Objective: "Review a document", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}},
		OperationStore: &operationStore{records: map[string]operations.Operation{}},
		ReportStore:    reportStore{}, ArtifactRestorer: artifactRestorer{},
	}
}
func newRunner(t *testing.T, config runner.Config, e executor) *runner.Runner {
	t.Helper()
	r, err := runner.NewRunner(t.Context(), e, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(t, r) })
	return r
}
func closeRunner(t *testing.T, r *runner.Runner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Errorf("close runner: %v", err)
	}
}
func start(t *testing.T, r *runner.Runner) operations.Operation {
	t.Helper()
	op, err := r.Start(t.Context(), commands.StartOperationCommand{MissionName: "review", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func assign(t *testing.T, r *runner.Runner, opID, action string) operations.Task {
	t.Helper()
	task, err := r.Assign(t.Context(), commands.AssignTaskCommand{OperationID: opID, AgentID: agentID, ActionName: action, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func receive(t *testing.T, calls <-chan invocation) invocation {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(5 * time.Second):
		t.Fatal("expected action dispatch")
		return invocation{}
	}
}
func awaitOperation(t *testing.T, r *runner.Runner, id string, ready func(operations.Operation) bool) operations.Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := r.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ready(op) {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not settle: %+v", op)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func output(name string, next ...actions.NextAction) actions.ActionOutput {
	return actions.ActionOutput{ActionName: name, Output: json.RawMessage(`{"ok":true}`), NextActions: next}
}
