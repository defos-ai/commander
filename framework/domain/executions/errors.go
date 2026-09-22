package executions

import (
	"errors"
	"fmt"
)

var (
	ErrUnknownExecution  = errors.New("unknown execution")
	ErrExecutionConflict = errors.New("execution already exists with different content")
	ErrIndeterminate     = errors.New("execution outcome is unknown; automatic retry is unsafe")
)

// CommandError is the serializable failure reported for an agent command.
type CommandError struct {
	ErrorType string `json:"error_type"`
	Message   string `json:"message"`
}

// CommandFailure preserves the remote agent's error and its command correlation.
type CommandFailure struct {
	AgentID   string
	CommandID string
	Detail    CommandError
}

func (e *CommandFailure) Error() string {
	return fmt.Sprintf("agent %s: %s: %s", e.AgentID, e.Detail.ErrorType, e.Detail.Message)
}
