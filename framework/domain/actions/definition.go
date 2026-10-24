package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

// Definition describes a capability independently of an executor or its readiness.
// Schemas are JSON objects; descriptions are optional. RequiredSecrets contains
// managed secret names, never values, resolved against the executing agent type.
type Definition struct {
	SessionMode     string          `json:"session_mode,omitempty"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	RequiredSecrets []string        `json:"required_secrets,omitempty"`
	InputSchema     json.RawMessage `json:"input_schema"`
	OutputSchema    json.RawMessage `json:"output_schema"`
}

// Validate checks shared metadata. Executors own schema compilation and execution
// checks; a valid definition does not establish an installed or ready capability.
func (d Definition) Validate() error {
	if err := d.ValidateSessionMode(); err != nil {
		return err
	}
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("action requires a name")
	}
	if err := d.ValidateRequiredSecrets(); err != nil {
		return err
	}
	for _, entry := range []struct {
		name   string
		schema json.RawMessage
	}{{"input", d.InputSchema}, {"output", d.OutputSchema}} {
		var value map[string]json.RawMessage
		if err := json.Unmarshal(entry.schema, &value); err != nil || value == nil {
			return fmt.Errorf("action %s schema must be a JSON object", entry.name)
		}
	}
	return nil
}

func (d Definition) ValidateSessionMode() error {
	if d.SessionMode != "" && d.SessionMode != "create" && d.SessionMode != "use" {
		return errors.New("session_mode must be create or use")
	}
	return nil
}

// ValidateRequiredSecrets also supports constructors that prepare schemas later.
func (d Definition) ValidateRequiredSecrets() error {
	seen := make(map[string]bool, len(d.RequiredSecrets))
	for _, name := range d.RequiredSecrets {
		if err := secrets.ValidateName(name); err != nil {
			return fmt.Errorf("required secret: %w", err)
		}
		if seen[name] {
			return errors.New("required secret names must be unique within an action")
		}
		seen[name] = true
	}
	return nil
}

func (d Definition) Clone() Definition {
	d.RequiredSecrets = slices.Clone(d.RequiredSecrets)
	d.InputSchema = bytes.Clone(d.InputSchema)
	d.OutputSchema = bytes.Clone(d.OutputSchema)
	return d
}
