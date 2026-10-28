package executions

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/id"
)

const MaxLogMessageSize = 2048

// Log is a progress observation within a dispatched execution. Its identity is
// bound by the runtime; it does not authorize retries or change execution state.
type Log struct {
	ID         string               `json:"id"`
	AgentID    string               `json:"agent_id"`
	CommandID  string               `json:"command_id"`
	ActionName string               `json:"action_name"`
	Work       messages.WorkContext `json:"work"`
	Message    string               `json:"message"`
	CreatedAt  time.Time            `json:"created_at"`
}

func (l Log) Validate() error {
	if !id.Valid(l.ID) {
		return errors.New("execution log ID must be a canonical UUID")
	}
	if !id.ValidAgent(l.AgentID) {
		return errors.New("execution log requires a valid agent ID")
	}
	if !id.Valid(l.CommandID) {
		return errors.New("execution log command ID must be a canonical UUID")
	}
	if strings.TrimSpace(l.ActionName) == "" || !utf8.ValidString(l.ActionName) {
		return errors.New("execution log action name must contain nonblank UTF-8 text")
	}
	if err := l.Work.Validate(); err != nil {
		return fmt.Errorf("execution log work: %w", err)
	}
	if !utf8.ValidString(l.Work.StepID) {
		return errors.New("execution log step ID must be valid UTF-8")
	}
	if l.CreatedAt.IsZero() {
		return errors.New("execution log timestamp must be set")
	}
	return ValidateLogMessage(l.Message)
}

func ValidateLogMessage(message string) error {
	if len(message) > MaxLogMessageSize {
		return fmt.Errorf("execution log message must not exceed %d bytes", MaxLogMessageSize)
	}
	if !utf8.ValidString(message) || strings.TrimSpace(message) == "" {
		return errors.New("execution log message must contain nonblank UTF-8 text")
	}
	return nil
}
