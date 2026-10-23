package build

import (
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/manifest"
	"github.com/defos-ai/commander/framework/domain/actions"
)

// ValidateActions checks requested contracts before the builder starts Codex.
// Secret declarations describe the generated agent, not the builder's needs.
func ValidateActions(definitions []actions.Definition) error {
	if len(definitions) == 0 || len(definitions) > 100 {
		return errors.New("actions must contain 1 to 100 definitions")
	}
	seen := make(map[string]bool, len(definitions))
	for i, definition := range definitions {
		if err := definition.Validate(); err != nil {
			return fmt.Errorf("actions[%d]: %w", i, err)
		}
		if err := (manifest.Action{Name: definition.Name, Description: definition.Description}).Validate(); err != nil {
			return fmt.Errorf("actions[%d]: %w", i, err)
		}
		if seen[definition.Name] {
			return errors.New("actions must have unique names")
		}
		seen[definition.Name] = true
		if _, err := action.ResolveSchema(definition.InputSchema); err != nil {
			return fmt.Errorf("actions[%d] input_schema: %w", i, err)
		}
		if _, err := action.ResolveSchema(definition.OutputSchema); err != nil {
			return fmt.Errorf("actions[%d] output_schema: %w", i, err)
		}
	}
	return nil
}
