package executions

import (
	"context"
	"encoding/json"

	"github.com/defos-ai/commander/framework/comms/messages"
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
	// SaveCheckpoint appends a named JSON object to an existing execution.
	// Same-name retries must preserve the saved value; conflicting data returns
	// ErrCheckpointConflict. New names after completion return ErrExecutionCompleted.
	// Writes are atomic with other checkpoints and Complete. Errors may leave the
	// write outcome uncertain: reconcile through Get or repeat the identical write
	// before proceeding. Get and Claim return independent checkpoint snapshots.
	// The controller validates the sender against the saved command before calling.
	SaveCheckpoint(ctx context.Context, agentID, commandID, name string, data json.RawMessage) error
	Complete(ctx context.Context, result messages.Message) error
	Get(ctx context.Context, agentID, commandID string) (Execution, error)
	// AppendLog retains an immutable observation separately from execution state.
	// Identical ID/content replays succeed; conflicting reuse returns ErrLogConflict.
	// Errors may follow a committed write: retry the same record or use GetLog to
	// inspect it. The controller verifies its saved claim before appending a log.
	AppendLog(ctx context.Context, log Log) error
	// GetLog returns ErrUnknownLog for a missing ID without creating a record.
	GetLog(ctx context.Context, logID string) (Log, error)
}
