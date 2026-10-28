package fakes

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type LogQueries struct {
	executions.ExecutionStore
	blocked  bool
	queries  chan executions.LogQuery
	finished chan error
}

func NewLogQueries(store executions.ExecutionStore, blocked bool) *LogQueries {
	return &LogQueries{ExecutionStore: store, blocked: blocked, queries: make(chan executions.LogQuery, 32), finished: make(chan error, 1)}
}

func (s *LogQueries) Queries() <-chan executions.LogQuery { return s.queries }
func (s *LogQueries) Finished() <-chan error              { return s.finished }

func (s *LogQueries) ListLogs(ctx context.Context, query executions.LogQuery) ([]executions.Log, error) {
	select {
	case s.queries <- query:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.blocked {
		<-ctx.Done()
		s.finished <- ctx.Err()
		return nil, ctx.Err()
	}
	if s.ExecutionStore == nil {
		return nil, errors.New("unexpected execution log read")
	}
	return s.ExecutionStore.ListLogs(ctx, query)
}

type LogOperations struct {
	operations.OperationStore
	reads chan string
}

func NewLogOperations(store operations.OperationStore) *LogOperations {
	return &LogOperations{OperationStore: store, reads: make(chan string, 32)}
}

func (s *LogOperations) Reads() <-chan string { return s.reads }

func (s *LogOperations) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	select {
	case s.reads <- operationID:
	case <-ctx.Done():
		return operations.Operation{}, ctx.Err()
	}
	if s.OperationStore == nil {
		return operations.Operation{}, errors.New("unexpected operation read")
	}
	return s.OperationStore.Get(ctx, operationID)
}
