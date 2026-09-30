package artifacts

import "context"

// ArtifactFileStore publishes bytes to shared storage before returning their
// descriptor. Get verifies content against the supplied size and digest.
type ArtifactFileStore interface {
	Put(ctx context.Context, artifactID string, source FileSource) (ArtifactFile, error)
	Get(ctx context.Context, artifactID string, file ArtifactFile) ([]byte, error)
}
