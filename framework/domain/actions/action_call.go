package actions

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
)

type ActionCall struct {
	ActionName string               `json:"action_name"`
	Input      json.RawMessage      `json:"input,omitempty"`
	Work       messages.WorkContext `json:"work,omitzero"`
}

func (c ActionCall) Validate() error {
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
