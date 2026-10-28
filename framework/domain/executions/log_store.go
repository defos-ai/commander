package executions

import "context"

// LogStore retains immutable observations separately from execution outcomes.
// Append accepts an identical ID/content replay and rejects conflicting reuse
// with ErrLogConflict. An error may follow a committed write: reconcile through
// Get or retry the identical record, preserving its ID and timestamp.
// The controller verifies the saved execution claim before appending a log;
// these methods validate record shape, not execution ownership or lifecycle.
type LogStore interface {
	Append(ctx context.Context, log Log) error
	// Get returns ErrUnknownLog for a missing ID without creating a record.
	Get(ctx context.Context, logID string) (Log, error)
}
