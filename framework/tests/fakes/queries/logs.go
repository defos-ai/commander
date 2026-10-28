package fakes

import (
	"context"
	"slices"

	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type LogScopeConfig struct {
	Operation            operations.Operation
	Task                 operations.Task
	OperationError       error
	TaskError            error
	CancelAfterOperation context.CancelFunc
	CancelAfterTask      context.CancelFunc
}

type LogScopeRead struct {
	Context     context.Context
	OperationID string
	TaskID      string
}

type LogScopes struct {
	operations.OperationStore
	config LogScopeConfig
	reads  []LogScopeRead
}

func NewLogScopes(config LogScopeConfig) *LogScopes {
	config.Operation, config.Task = config.Operation.Clone(), config.Task.Clone()
	return &LogScopes{config: config}
}

func (s *LogScopes) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	s.reads = append(s.reads, LogScopeRead{Context: ctx, OperationID: operationID})
	if s.config.CancelAfterOperation != nil {
		s.config.CancelAfterOperation()
	}
	return s.config.Operation.Clone(), s.config.OperationError
}

func (s *LogScopes) GetTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
	s.reads = append(s.reads, LogScopeRead{Context: ctx, OperationID: operationID, TaskID: taskID})
	if s.config.CancelAfterTask != nil {
		s.config.CancelAfterTask()
	}
	return s.config.Task.Clone(), s.config.TaskError
}

func (s *LogScopes) Reads() []LogScopeRead { return slices.Clone(s.reads) }

type LogExecutionConfig struct {
	Items           []executions.Log
	Error           error
	CancelAfterList context.CancelFunc
}

type LogListRead struct {
	Context  context.Context
	Criteria executions.LogQuery
}

type LogExecutions struct {
	executions.ExecutionStore
	config LogExecutionConfig
	reads  []LogListRead
}

func NewLogExecutions(config LogExecutionConfig) *LogExecutions {
	config.Items = slices.Clone(config.Items)
	return &LogExecutions{config: config}
}

func (s *LogExecutions) ListLogs(ctx context.Context, criteria executions.LogQuery) ([]executions.Log, error) {
	s.reads = append(s.reads, LogListRead{Context: ctx, Criteria: criteria})
	if s.config.CancelAfterList != nil {
		s.config.CancelAfterList()
	}
	return slices.Clone(s.config.Items), s.config.Error
}

func (s *LogExecutions) Reads() []LogListRead { return slices.Clone(s.reads) }
