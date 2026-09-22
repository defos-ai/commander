package missionsmcp

import (
	"context"

	"commander/framework/artifact"
)

func readArtifact(ctx context.Context, config Config, artifactID string) (artifact.Ref, []byte, error) {
	ref, err := config.ExecutionStore.FindArtifact(ctx, artifactID)
	if err != nil {
		return artifact.Ref{}, nil, err
	}
	data, err := config.ArtifactStore.Get(ctx, ref)
	return ref, data, err
}
