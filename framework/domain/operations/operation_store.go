package operations

import (
	"context"
)

// OperationStore saves complete snapshots atomically. Create requires version 1;
// Update replaces expectedVersion with expectedVersion+1 or returns a conflict.
// Implementations must return and retain independent copies.
type OperationStore interface {
	Create(context.Context, Operation) error
	Get(context.Context, string) (Operation, error)
	List(context.Context) ([]Operation, error)
	Update(context.Context, Operation, uint64) error
}
