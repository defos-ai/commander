package controller

import (
	"context"
	"slices"

	"commander/framework/artifact"
)

// RestoreArtifacts restores validated references from persisted operation
// results. Storage remains responsible for checking content when it is read.
func (c *Controller) RestoreArtifacts(refs []artifact.Ref) error {
	return c.restoreArtifacts(c.state.Context, refs)
}

func (c *Controller) restoreArtifacts(ctx context.Context, refs []artifact.Ref) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(refs) == 0 {
		return c.state.CheckOpen()
	}
	refs = slices.Clone(refs)
	_, err := submit(ctx, c, func(context.Context) (struct{}, error) {
		return struct{}{}, c.state.RestoreArtifacts(refs)
	})
	return err
}

func (c *Controller) Artifact(artifactID string) (artifact.Ref, error) {
	return c.state.Artifact(artifactID)
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
