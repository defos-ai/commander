package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"strings"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/observations"
)

type ActionOutput struct {
	Observations *observations.Batch  `json:"observations,omitempty"`
	Session      *sessions.Session    `json:"session,omitempty"`
	ActionName   string               `json:"action_name"`
	Output       json.RawMessage      `json:"output"`
	Artifacts    []artifacts.Artifact `json:"artifacts,omitempty"`
	NextActions  []NextAction         `json:"next_actions,omitempty"`
}

func (o ActionOutput) Validate() error {
	if err := o.Observations.Validate(); err != nil {
		return err
	}
	if o.Session != nil {
		if err := o.Session.Validate(); err != nil {
			return err
		}
	}
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
	o.Observations = o.Observations.Clone()
	if o.Session != nil {
		v := o.Session.Clone()
		o.Session = &v
	}
	o.Output = bytes.Clone(o.Output)
	o.NextActions = CloneNextActions(o.NextActions)
	o.Artifacts = artifacts.CloneAll(o.Artifacts)
	return o
}
