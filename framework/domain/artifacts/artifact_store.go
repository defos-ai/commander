package artifacts

import "context"

// ArtifactStore persists accepted metadata in the controller database.
// Create is immutable and idempotent: the same ID and metadata succeeds;
// a conflicting replay returns ErrArtifactConflict.
type ArtifactStore interface {
	Create(ctx context.Context, value Artifact) error
	Get(ctx context.Context, artifactID string) (Artifact, error)
}
