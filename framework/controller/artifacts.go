package controller

import (
	"context"

	"commander/framework/artifact"
)

// RestoreArtifacts restores validated references from persisted operation
// results. Storage remains responsible for checking content when it is read.
func (c *Controller) RestoreArtifacts(refs []artifact.Ref) error {
	return c.state.RestoreArtifacts(refs)
}

func (c *Controller) Artifact(artifactID string) (artifact.Ref, error) {
	c.state.Mu.RLock()
	defer c.state.Mu.RUnlock()
	if c.state.Closed {
		return artifact.Ref{}, ErrControllerClosed
	}
	ref, ok := c.state.Artifacts[artifactID]
	if !ok {
		return artifact.Ref{}, ErrUnknownArtifact
	}
	return ref, nil
}

func (c *Controller) ReadArtifact(ctx context.Context, artifactID string) (artifact.Ref, []byte, error) {
	if err := ctx.Err(); err != nil {
		return artifact.Ref{}, nil, err
	}
	ref, err := c.Artifact(artifactID)
	if err != nil {
		return artifact.Ref{}, nil, err
	}
	data, err := c.artifactStore.Get(ctx, ref)
	if err != nil {
		return artifact.Ref{}, nil, err
	}
	return ref, data, nil
}
