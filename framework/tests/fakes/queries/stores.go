package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type OperationStore struct {
	operations.OperationStore
	ReadOperation func(context.Context, string) (operations.Operation, error)
	ReadAll       func(context.Context) ([]operations.Operation, error)
	ReadTask      func(context.Context, string, string) (operations.Task, error)
	ReadTasks     func(context.Context, string, string, int) ([]operations.Task, error)
	ReadEvents    func(context.Context, string, uint64, int) ([]operations.Event, error)
}

func (s OperationStore) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	return s.ReadOperation(ctx, operationID)
}

func (s OperationStore) List(ctx context.Context) ([]operations.Operation, error) {
	return s.ReadAll(ctx)
}

func (s OperationStore) GetTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
	return s.ReadTask(ctx, operationID, taskID)
}

func (s OperationStore) ListTasks(ctx context.Context, operationID, after string, limit int) ([]operations.Task, error) {
	return s.ReadTasks(ctx, operationID, after, limit)
}

func (s OperationStore) ListEvents(ctx context.Context, operationID string, after uint64, limit int) ([]operations.Event, error) {
	return s.ReadEvents(ctx, operationID, after, limit)
}

type AgentStore struct {
	agents.AgentStore
	ReadAll func(context.Context) ([]agents.Agent, error)
}

func (s AgentStore) List(ctx context.Context) ([]agents.Agent, error) {
	return s.ReadAll(ctx)
}
