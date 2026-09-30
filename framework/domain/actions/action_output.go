package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"commander/framework/domain"
	"commander/framework/domain/artifacts"
)

type ActionOutput struct {
	ActionName  string               `json:"action_name"`
	Output      json.RawMessage      `json:"output"`
	Artifacts   []artifacts.Artifact `json:"artifacts,omitempty"`
	NextActions []NextAction         `json:"next_actions,omitempty"`
}

func (o ActionOutput) Validate() error {
	if err := ValidateNextActions(o.NextActions); err != nil {
		return err
	}
	if strings.TrimSpace(o.ActionName) == "" {
		return errors.New("action_name must not be blank")
	}
	if err := domain.ValidateObject(o.Output); err != nil {
		return fmt.Errorf("action output: %w", err)
	}
	for _, value := range o.Artifacts {
		if err := value.Validate(); err != nil {
			return fmt.Errorf("artifact: %w", err)
		}
	}
	return nil
}

func (o ActionOutput) Clone() ActionOutput {
	o.Output = bytes.Clone(o.Output)
	o.NextActions = CloneNextActions(o.NextActions)
	o.Artifacts = artifacts.CloneAll(o.Artifacts)
	return o
}
