package actions

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/assessments"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

type ActionCall struct {
	Assessment *assessments.Snapshot `json:"assessment,omitempty"`
	Session    *sessions.Binding     `json:"session,omitempty"`
	ActionName string                `json:"action_name"`
	Input      json.RawMessage       `json:"input,omitempty"`
	Work       messages.WorkContext  `json:"work,omitzero"`
}

func (c ActionCall) Validate() error {
	if err := c.Assessment.Validate(); err != nil {
		return err
	}
	if c.Session != nil {
		if err := c.Session.Validate(); err != nil {
			return err
		}
	}
	if err := c.Work.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.ActionName) == "" {
		return errors.New("action_name must not be blank")
	}
	if len(c.Input) != 0 {
		return domain.ValidateObject(c.Input)
	}
	return nil
}
