package fakes

import (
	"bytes"
	"context"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type LifecycleExecutions struct {
	executions.ExecutionStore
	record atomic.Pointer[executions.Execution]
}

func (s *LifecycleExecutions) Claim(ctx context.Context, command messages.Message) (executions.Execution, bool, error) {
	if err := ctx.Err(); err != nil {
		return executions.Execution{}, false, err
	}
	next := executions.Execution{Command: command, StartedAt: command.CreatedAt}
	if err := next.Validate(); err != nil {
		return executions.Execution{}, false, err
	}
	next.Command.Payload = bytes.Clone(command.Payload)
	if s.record.CompareAndSwap(nil, &next) {
		return cloneLifecycleExecution(next), true, nil
	}
	previous := s.record.Load()
	if previous.Command.ID != command.ID || previous.Command.AgentID != command.AgentID || previous.Command.Type != command.Type || (previous.Command.OperationID != command.OperationID || previous.Command.TaskID != command.TaskID) || !bytes.Equal(previous.Command.Payload, command.Payload) {
		return executions.Execution{}, false, executions.ErrExecutionConflict
	}
	return cloneLifecycleExecution(*previous), false, nil
}

func (s *LifecycleExecutions) Get(ctx context.Context, agentID, commandID string) (executions.Execution, error) {
	if err := ctx.Err(); err != nil {
		return executions.Execution{}, err
	}
	record := s.record.Load()
	if record == nil || record.Command.AgentID != agentID || record.Command.ID != commandID {
		return executions.Execution{}, executions.ErrUnknownExecution
	}
	return cloneLifecycleExecution(*record), nil
}

func (s *LifecycleExecutions) Complete(ctx context.Context, result messages.Message) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		previous := s.record.Load()
		if previous == nil {
			return executions.ErrUnknownExecution
		}
		if previous.Result != nil {
			if reflect.DeepEqual(*previous.Result, result) {
				return nil
			}
			return executions.ErrExecutionConflict
		}
		next := cloneLifecycleExecution(*previous)
		result.Payload = bytes.Clone(result.Payload)
		now := time.Now().UTC()
		next.Result, next.CompletedAt = &result, &now
		if err := next.Validate(); err != nil {
			return err
		}
		if s.record.CompareAndSwap(previous, &next) {
			return nil
		}
	}
}

func cloneLifecycleExecution(record executions.Execution) executions.Execution {
	record.Command.Payload = bytes.Clone(record.Command.Payload)
	if record.Result != nil {
		result, completed := *record.Result, *record.CompletedAt
		result.Payload = bytes.Clone(result.Payload)
		record.Result, record.CompletedAt = &result, &completed
	}
	return record
}

type DrainingOperations struct {
	operations.OperationStore
	finishGate atomic.Pointer[Gate]
}

func (s *DrainingOperations) BlockNextFinish(gate *Gate) { s.finishGate.Store(gate) }

func (s *DrainingOperations) FinishTask(ctx context.Context, operationID string, result operations.TaskResult) (operations.Operation, error) {
	s.finishGate.Swap(nil).wait()
	return s.OperationStore.FinishTask(ctx, operationID, result)
}
