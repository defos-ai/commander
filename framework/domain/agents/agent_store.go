package agents

import (
	"context"
	"time"
)

// AgentStore persists observations keyed by agent ID.
// Implementations must be safe for concurrent use, honor context cancellation,
// and return independent snapshots. Get returns ErrUnknownAgent when absent.
// Upsert returns only after the observation is available to subsequent readers.
type AgentStore interface {
	Upsert(ctx context.Context, record Agent) error
	Get(ctx context.Context, agentID string) (Agent, error)
	List(ctx context.Context) ([]Agent, error)
	// DeleteExpired removes records whose last heartbeat is at or before cutoff.
	// The comparison and removal must be atomic with Upsert. A zero heartbeat
	// has no lease and expires; other observations do not renew a lease.
	// Return any successfully removed records even if a later removal fails.
	DeleteExpired(ctx context.Context, cutoff time.Time) ([]Agent, error)
}
