package actions

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

type ActionCall struct {
	Session     *sessions.Binding `json:"session,omitempty"`
	ActionName  string            `json:"action_name"`
	Input       json.RawMessage   `json:"input,omitempty"`
	OperationID string            `json:"operation_id,omitempty"`
	TaskID      string            `json:"task_id,omitempty"`
}

func (c ActionCall) Validate() error {
	if c.Session != nil {
		if err := c.Session.Validate(); err != nil {
			return err
		}
	}
	if (c.OperationID != "" || c.TaskID != "") && (!id.Valid(c.OperationID) || !id.Valid(c.TaskID)) {
		return errors.New("action call requires both operation and task UUIDs, or neither")
	}
	if strings.TrimSpace(c.ActionName) == "" {
		return errors.New("action_name must not be blank")
	}
	if len(c.Input) != 0 {
		return domain.ValidateObject(c.Input)
	}
	return nil
}
