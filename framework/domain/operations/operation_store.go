package operations

import "context"

// Implementations retain and return independent copies. Update applies a mutation
// atomically to the latest operation. Errors other than ErrWriteUncertain leave it unchanged.
// ErrWriteUncertain means the new snapshot is visible but durability is unconfirmed;
// Update returns that snapshot. Pause new work until recovery reconciles
// persisted state. Providers may retry the callback; it must not perform I/O or
// other external side effects.
type OperationStore interface {
	Create(context.Context, Operation) error
	Get(context.Context, string) (Operation, error)
	List(context.Context) ([]Operation, error)
	// Task queries are scoped to one operation. A missing operation returns
	// ErrUnknownOperation; GetTask returns ErrUnknownTask when its task is absent.
	GetTask(ctx context.Context, operationID, taskID string) (Task, error)
	// ListTasks returns up to limit tasks in assignment order after afterTaskID.
	// An empty cursor starts the first page; an unknown cursor returns ErrUnknownTask.
	// The limit must be positive. Status updates do not change assignment order.
	ListTasks(ctx context.Context, operationID, afterTaskID string, limit int) ([]Task, error)
	// ListEvents returns up to limit events in ascending sequence order, strictly
	// after afterSequence. The limit must be positive. A cursor at or beyond the
	// latest sequence returns an empty page; an unknown operation returns ErrUnknownOperation.
	ListEvents(ctx context.Context, operationID string, afterSequence uint64, limit int) ([]Event, error)
	Update(context.Context, string, func(*Operation) error) (Operation, error)
}
