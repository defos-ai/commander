package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/executions"
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
