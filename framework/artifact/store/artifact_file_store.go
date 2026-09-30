package store

import (
	"context"

	"commander/framework/artifact"
)

// ArtifactFileStore publishes bytes to shared storage before returning their
// descriptor. Get verifies content against the supplied size and digest.
type ArtifactFileStore interface {
	Put(ctx context.Context, artifactID string, source artifact.FileSource) (artifact.ArtifactFile, error)
	Get(ctx context.Context, artifactID string, file artifact.ArtifactFile) ([]byte, error)
}
