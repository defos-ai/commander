package missions

import (
	"context"
)

// MissionStore holds reusable missions. Each operation keeps its own snapshot;
// editing or deleting catalog entries affects future operations only. Stores
// validate definitions, honor cancellation and return independent snapshots.
type MissionStore interface {
	// Create atomically saves a mission only if its exact name is absent.
	// An existing name returns ErrMissionExists, even for an identical definition;
	// concurrent creates cannot overwrite the winner. After a write error, callers
	// must read the catalog to reconcile the outcome before retrying.
	Create(context.Context, Mission) error

	// Update replaces an existing definition atomically; it never recreates a
	// deleted name. Missing names return ErrUnknownMission.
	Update(context.Context, Mission) error
	Upsert(context.Context, Mission) error

	// Delete removes the exact name, returning ErrUnknownMission if absent.
	// Existing operation snapshots are retained. Reconcile a failed write by
	// reading the catalog before retrying.
	Delete(context.Context, string) error
	Get(context.Context, string) (Mission, error)
	List(context.Context) ([]Mission, error)
}
