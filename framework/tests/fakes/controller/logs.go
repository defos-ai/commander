package fakes

import (
	"context"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/domain/executions"
)

type LoggedExecutions struct {
	*CheckpointExecutions
	mu             sync.Mutex
	records        map[string]executions.Log
	appendError    error
	commitOnError  bool
	readError      error
	readback       *executions.Log
	discardWrites  bool
	appendGate     *Gate
	readGate       *Gate
	appends, reads int
	deadline       time.Time
}

var _ executions.ExecutionStore = (*LoggedExecutions)(nil)

type ExecutionLogSnapshot struct {
	Records  map[string]executions.Log
	Appends  int
	Reads    int
	Deadline time.Time
}

func NewLoggedExecutions(record executions.Execution) *LoggedExecutions {
	return &LoggedExecutions{CheckpointExecutions: NewCheckpointExecutions(record), records: make(map[string]executions.Log)}
}

func (s *LoggedExecutions) AppendLog(ctx context.Context, log executions.Log) error {
	s.mu.Lock()
	s.appends++
	s.deadline, _ = ctx.Deadline()
	gate := s.appendGate
	s.appendGate = nil
	s.mu.Unlock()
	gate.wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := log.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appendError != nil && !s.commitOnError {
		return s.appendError
	}
	if previous, exists := s.records[log.ID]; exists && !previous.Equal(log) {
		return executions.ErrLogConflict
	}
	if !s.discardWrites {
		s.records[log.ID] = log
	}
	return s.appendError
}

func (s *LoggedExecutions) GetLog(ctx context.Context, id string) (executions.Log, error) {
	s.mu.Lock()
	s.reads++
	gate := s.readGate
	s.readGate = nil
	s.mu.Unlock()
	gate.wait()
	if err := ctx.Err(); err != nil {
		return executions.Log{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readError != nil {
		return executions.Log{}, s.readError
	}
	if s.readback != nil {
		return *s.readback, nil
	}
	if log, exists := s.records[id]; exists {
		return log, nil
	}
	return executions.Log{}, executions.ErrUnknownLog
}

func (s *LoggedExecutions) LogSnapshot() ExecutionLogSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := ExecutionLogSnapshot{Records: make(map[string]executions.Log, len(s.records)), Appends: s.appends, Reads: s.reads, Deadline: s.deadline}
	for id, log := range s.records {
		result.Records[id] = log
	}
	return result
}

func (s *LoggedExecutions) FailLogAppend(err error, commit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendError, s.commitOnError = err, commit
}

func (s *LoggedExecutions) FailLogRead(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readError = err
}

func (s *LoggedExecutions) ReadLogAs(log executions.Log) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readback = &log
}

func (s *LoggedExecutions) DiscardLogAppends() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discardWrites = true
}

func (s *LoggedExecutions) BlockNextLogAppend(gate *Gate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendGate = gate
}

func (s *LoggedExecutions) BlockNextLogRead(gate *Gate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readGate = gate
}
