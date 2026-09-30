package store

import (
	"context"

	"commander/framework/artifact"
)

// ArtifactStore persists accepted metadata in the controller database.
// Create is immutable and idempotent: the same ID and metadata succeeds;
// a conflicting replay returns artifact.ErrArtifactConflict.
type ArtifactStore interface {
	Create(ctx context.Context, value artifact.Artifact) error
	Get(ctx context.Context, artifactID string) (artifact.Artifact, error)
}
