package queries

import (
	"context"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func (q Operations) ListLogs(ctx context.Context, criteria executions.LogQuery) ([]executions.Log, error) {
	if ctx == nil {
		return nil, errors.New("operation log query requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !id.Valid(criteria.OperationID) {
		return nil, fmt.Errorf("%w: log operation ID must be a canonical UUID", domain.ErrInvalidInput)
	}
	if err := criteria.Validate(); err != nil {
		return nil, err
	}
	if q.Store == nil || q.Executions == nil {
		return nil, errors.New("operation log query requires operation and execution stores")
	}
	operation, err := q.Store.Get(ctx, criteria.OperationID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if operation.ID != criteria.OperationID {
		return nil, operations.ErrUnknownOperation
	}
	if criteria.TaskID != "" {
		task, err := q.Store.GetTask(ctx, criteria.OperationID, criteria.TaskID)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if task.ID != criteria.TaskID || task.OperationID != criteria.OperationID {
			return nil, operations.ErrUnknownTask
		}
	}
	items, err := q.Executions.ListLogs(ctx, criteria)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if items == nil {
		items = []executions.Log{}
	}
	return items, nil
}
