package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"commander/framework/artifact"
	artifactstore "commander/framework/artifact/store"
	"commander/framework/domain"
)

const DefaultInlineOutputLimit = 100_000

// ActionOutput carries structured JSON inline or by reference, and file references.
// Files are stored even if they are smaller than the inline JSON limit.
type ActionOutput struct {
	ActionName     string          `json:"action_name"`
	Output         json.RawMessage `json:"output,omitempty"`
	OutputArtifact *artifact.Ref   `json:"output_artifact,omitempty"`
	Artifacts      []artifact.Ref  `json:"artifacts,omitempty"`
	NextActions    []NextAction    `json:"next_actions,omitempty"`
}

func (o ActionOutput) Validate() error {
	if err := ValidateNextActions(o.NextActions); err != nil {
		return err
	}
	if strings.TrimSpace(o.ActionName) == "" {
		return errors.New("action_name must not be blank")
	}
	if (len(o.Output) == 0) == (o.OutputArtifact == nil) {
		return errors.New("provide exactly one of output or output_artifact")
	}
	if o.OutputArtifact != nil {
		if err := o.OutputArtifact.Validate(); err != nil {
			return fmt.Errorf("output artifact: %w", err)
		}
	}
	for _, ref := range o.Artifacts {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("attachment: %w", err)
		}
	}
	if len(o.Output) > 0 {
		return domain.ValidateObject(o.Output)
	}
	return nil
}

func (o ActionOutput) ResolveOutput(ctx context.Context, store artifactstore.ArtifactStore) (json.RawMessage, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if len(o.Output) > 0 {
		return append(json.RawMessage(nil), o.Output...), nil
	}
	if store == nil {
		return nil, errors.New("an artifact store is required to resolve output")
	}
	data, err := store.Get(ctx, *o.OutputArtifact)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateObject(data); err != nil {
		return nil, fmt.Errorf("stored output: %w", err)
	}
	return json.RawMessage(data), nil
}
