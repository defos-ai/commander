package operations

import "context"

// Implementations retain and return independent copies. Update applies a mutation
// atomically to the latest operation, leaving it unchanged on error. Providers may
// retry the callback; it must not perform I/O or other external side effects.
type OperationStore interface {
	Create(context.Context, Operation) error
	Get(context.Context, string) (Operation, error)
	List(context.Context) ([]Operation, error)
	Update(context.Context, string, func(*Operation) error) (Operation, error)
}
