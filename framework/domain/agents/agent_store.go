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
// record only when the supplied snapshot matches, retaining monotonic LastSeen.
// Otherwise equal observations are stale. Lease-expiry marks retain the original
// ordering rule. Upsert preserves controller-owned stop intent independently of
// the supplied observation.
type AgentStore interface {
	Upsert(ctx context.Context, record Agent) error
	// RequestStop atomically retains the first controller request for a known
	// agent, or returns its existing request/confirmed stopped state unchanged.
	// Observation writes cannot create, replace or clear this request.
	RequestStop(ctx context.Context, agentID, commandID string, requestedAt time.Time) (Agent, error)
	Get(ctx context.Context, agentID string) (Agent, error)
	List(ctx context.Context) ([]Agent, error)
	// DeleteExpired removes records whose last heartbeat is at or before cutoff.
	// The comparison and removal must be atomic with Upsert. A zero heartbeat
	// has no lease and expires; other observations do not renew a lease.
	// Return any successfully removed records even if a later removal fails.
	// Hide expired records from Get/List, retaining their ordering mark until
	// ObservedAt is at or before cutoff. Lifecycle consumers must discard messages
	// outside that same heartbeat window before attempting Upsert.
	// Retain unconfirmed stop requests so lease expiry cannot remove their fence.
	DeleteExpired(ctx context.Context, cutoff time.Time) ([]Agent, error)
}
