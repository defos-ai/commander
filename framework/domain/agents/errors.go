package agents

import "errors"

var (
	ErrUnknownAgent    = errors.New("agent has not announced itself")
	ErrAgentNotRunning = errors.New("agent is not available for commands")
)
