package executions

import (
	"context"

	"commander/framework/comms/messages"
)

// ExecutionStore belongs to the controller database. Claim reserves a command
// before publication and returns true only to the caller creating the reservation.
// Complete persists a received terminal response before the controller acknowledges
// its delivery. Reusing a command ID with different logical content is a conflict;
// changed CreatedAt/ExpiresAt alone do not change that content or replace the
// original admission deadline. Completed outcomes are
// immutable; repeating Complete with the same response is harmless.
type ExecutionStore interface {
	Claim(ctx context.Context, command messages.Message) (Execution, bool, error)
	Complete(ctx context.Context, result messages.Message) error
	Get(ctx context.Context, agentID, commandID string) (Execution, error)
}
