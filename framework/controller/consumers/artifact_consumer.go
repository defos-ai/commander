package consumers

import (
	"context"
	"errors"
	"fmt"

	"commander/framework/artifact"
	artifactstore "commander/framework/artifact/store"
	"commander/framework/comms/messages"
	"commander/framework/domain"
	"commander/framework/domain/actions"
	"commander/framework/domain/executions"
)

// ArtifactConsumer indexes artifacts from an already accepted, durable action result.
// Replaying that result repairs metadata if an earlier database write failed.
type ArtifactConsumer struct{ Store artifactstore.ArtifactStore }

func (c ArtifactConsumer) Consume(ctx context.Context, message messages.Message) error {
	if message.Type != messages.ActionSucceeded {
		return nil
	}
	var output actions.ActionOutput
	if err := domain.Decode(message.Payload, &output); err != nil {
		return InvalidMessage(err)
	}
	records, err := executions.OutputArtifacts(message, output)
	if err != nil {
		return InvalidMessage(err)
	}
	for _, record := range records {
		if err := c.Store.Create(ctx, record); err != nil {
			if errors.Is(err, artifact.ErrArtifactConflict) {
				return InvalidMessage(err)
			}
			return fmt.Errorf("persist artifact metadata: %w", err)
		}
	}
	return nil
}
