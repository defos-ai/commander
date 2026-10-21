package missions

import (
	"context"
)

// MissionStore holds reusable missions. Each operation keeps its own snapshot;
// editing the catalog affects future operations only. Stores validate definitions,
// honor cancellation and return independent snapshots.
type MissionStore interface {
	// Create atomically saves a mission only if its exact name is absent.
	// An existing name returns ErrMissionExists, even for an identical definition;
	// concurrent creates cannot overwrite the winner. After a write error, callers
	// must read the catalog to reconcile the outcome before retrying.
	Create(context.Context, Mission) error

	// Upsert replaces a definition by name and is used for host-managed imports.
	Upsert(context.Context, Mission) error
	Get(context.Context, string) (Mission, error)
	List(context.Context) ([]Mission, error)
}
