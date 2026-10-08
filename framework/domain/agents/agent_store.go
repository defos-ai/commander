package agents

import (
	"context"
	"time"
)

// AgentStore persists observations keyed by agent ID.
// Implementations must be safe for concurrent use, honor context cancellation,
// and return independent snapshots. Get returns ErrUnknownAgent when absent.
// Upsert returns only after the observation is available to subsequent readers.
// Once ObservedAt is set, older observations return ErrStaleObservation. An equal
// mark may atomically renew a strictly newer LastHeartbeat on a live, non-stopped
// record, retaining its current snapshot and monotonic LastSeen. Otherwise equal
// observations are stale. Lease-expiry marks retain the original ordering rule.
type AgentStore interface {
	Upsert(ctx context.Context, record Agent) error
	Get(ctx context.Context, agentID string) (Agent, error)
	List(ctx context.Context) ([]Agent, error)
	// DeleteExpired removes records whose last heartbeat is at or before cutoff.
	// The comparison and removal must be atomic with Upsert. A zero heartbeat
	// has no lease and expires; other observations do not renew a lease.
	// Return any successfully removed records even if a later removal fails.
	// Hide expired records from Get/List, retaining their ordering mark until
	// ObservedAt is at or before cutoff. Lifecycle consumers must discard messages
	// outside that same heartbeat window before attempting Upsert.
	DeleteExpired(ctx context.Context, cutoff time.Time) ([]Agent, error)
}
