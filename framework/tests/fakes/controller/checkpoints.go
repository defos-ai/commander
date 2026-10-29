package fakes

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/defos-ai/commander/framework/domain/executions"
)

type CheckpointExecutions struct {
	executions.ExecutionStore
	record       executions.Execution
	saveError    error
	readError    error
	ignoreLookup bool
	saves        int
}

func (store *CheckpointExecutions) Get(ctx context.Context, agentID, commandID string) (executions.Execution, error) {
	if err := ctx.Err(); err != nil {
		return executions.Execution{}, err
	}
	if store.readError != nil {
		return executions.Execution{}, store.readError
	}
	if !store.ignoreLookup && (agentID != store.record.Command.AgentID || commandID != store.record.Command.ID) {
		return executions.Execution{}, executions.ErrUnknownExecution
	}
	record := store.record
	record.Command.Payload = bytes.Clone(record.Command.Payload)
	record.Checkpoints = make(map[string]json.RawMessage, len(store.record.Checkpoints))
	for name, data := range store.record.Checkpoints {
		record.Checkpoints[name] = bytes.Clone(data)
	}
	if record.Result != nil {
		result := *record.Result
		result.Payload = bytes.Clone(result.Payload)
		record.Result = &result
		completed := *record.CompletedAt
		record.CompletedAt = &completed
	}
	return record, nil
}

func (store *CheckpointExecutions) SaveCheckpoint(ctx context.Context, agentID, commandID, name string, data json.RawMessage) error {
	record, err := store.Get(ctx, agentID, commandID)
	if err != nil {
		return err
	}
	store.saves++
	if _, err := record.RecordCheckpoint(name, data); err != nil {
		return err
	}
	store.record = record
	return store.saveError
}

func NewCheckpointExecutions(record executions.Execution) *CheckpointExecutions {
	return &CheckpointExecutions{record: record}
}
func (s *CheckpointExecutions) Snapshot() executions.Execution {
	copy := s.record
	copy.Command.Payload = bytes.Clone(copy.Command.Payload)
	copy.Checkpoints = make(map[string]json.RawMessage, len(s.record.Checkpoints))
	for k, v := range s.record.Checkpoints {
		copy.Checkpoints[k] = bytes.Clone(v)
	}
	return copy
}
func (s *CheckpointExecutions) Change(change func(*executions.Execution)) {
	record := s.Snapshot()
	change(&record)
	s.record = record
}
func (s *CheckpointExecutions) IgnoreLookup()      { s.ignoreLookup = true }
func (s *CheckpointExecutions) FailSave(err error) { s.saveError = err }
func (s *CheckpointExecutions) FailRead(err error) { s.readError = err }
func (s *CheckpointExecutions) Saves() int         { return s.saves }

func (s *CheckpointExecutions) AddCheckpoint(name string, data json.RawMessage) (bool, error) {
	return s.record.RecordCheckpoint(name, data)
}
