package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type LogQueries struct {
	executions.ExecutionStore
	err   error
	calls Calls
	query executions.LogQuery
}

func NewLogQueries(err error) *LogQueries { return &LogQueries{err: err} }

func (s *LogQueries) ListLogs(ctx context.Context, query executions.LogQuery) ([]executions.Log, error) {
	s.calls.record(ctx)
	s.query = query
	return []executions.Log{}, s.err
}

func (s *LogQueries) Snapshot() (Calls, executions.LogQuery) { return s.calls, s.query }

type LogOperationReads struct {
	operations.OperationStore
	calls Calls
}

func (s *LogOperationReads) Get(ctx context.Context, _ string) (operations.Operation, error) {
	s.calls.record(ctx)
	return operations.Operation{}, operations.ErrUnknownOperation
}

func (s *LogOperationReads) Snapshot() Calls { return s.calls }
