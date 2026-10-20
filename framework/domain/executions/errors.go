package executions

import (
	"errors"
	"fmt"
)

var (
	ErrUnknownExecution   = errors.New("unknown execution")
	ErrExecutionConflict  = errors.New("execution already exists with different content")
	ErrCheckpointConflict = errors.New("checkpoint already exists with different content")
	ErrExecutionCompleted = errors.New("completed execution cannot accept new checkpoints")
	ErrIndeterminate      = errors.New("execution outcome is unknown; automatic retry is unsafe")
)

type CommandError struct {
	ErrorType string `json:"error_type"`
	Message   string `json:"message"`
}

type CommandFailure struct {
	AgentID   string
	CommandID string
	Detail    CommandError
}

func (e *CommandFailure) Error() string {
	return fmt.Sprintf("agent %s: %s: %s", e.AgentID, e.Detail.ErrorType, e.Detail.Message)
}
