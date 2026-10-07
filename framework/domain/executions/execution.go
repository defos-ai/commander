// Package executions records controller dispatch reservations and received action
// outcomes independently of message delivery.
package executions

import (
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
)

// Execution is a durable dispatch reservation. StartedAt records when
// the reservation was created, not when an agent began execution. A missing
// Result means the remote outcome is unknown. Queue recovery may redeliver the
// exact envelope to the same live instance; it never authorizes a new execution ID.
type Execution struct {
	Command     messages.Message  `json:"command"`
	Result      *messages.Message `json:"result,omitempty"`
	StartedAt   time.Time         `json:"started_at"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
}

func (r Execution) Validate() error {
	if err := r.Command.Validate(); err != nil {
		return fmt.Errorf("execution command: %w", err)
	}
	if r.Command.Type != messages.RunAction || r.Command.CorrelationID != "" {
		return errors.New("execution requires an action command without a correlation ID")
	}
	if r.StartedAt.IsZero() {
		return errors.New("execution start time is required")
	}
	if (r.Result == nil) != (r.CompletedAt == nil) {
		return errors.New("execution result and completion time must be set together")
	}
	if r.Result == nil {
		return nil
	}
	if err := r.Result.Validate(); err != nil {
		return fmt.Errorf("execution result: %w", err)
	}
	if r.Result.Type != messages.ActionSucceeded && r.Result.Type != messages.CommandFailed {
		return errors.New("execution result must indicate success or failure")
	}
	if r.Result.AgentID != r.Command.AgentID || r.Result.CorrelationID != r.Command.ID || r.Result.Work != r.Command.Work {
		return errors.New("execution result does not belong to its command")
	}
	if r.CompletedAt.IsZero() {
		return errors.New("execution completion time must not be zero")
	}
	return nil
}
