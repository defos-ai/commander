// Package store persists and retrieves artifacts.
package store

import (
	"context"

	"commander/framework/artifact"
)

// ArtifactStore publishes and retrieves artifacts. Put returns only after both
// content and metadata are available to subsequent reads.
type ArtifactStore interface {
	Put(context.Context, artifact.Artifact, artifact.Owner) (artifact.Ref, error)
	Get(context.Context, artifact.Ref) ([]byte, error)
}
