package agents

import "errors"

var (
	ErrStaleObservation = errors.New("agent observation is not newer than the saved observation")
	ErrUnknownAgent     = errors.New("agent has not announced itself")
	ErrAgentNotRunning  = errors.New("agent is not available for commands")
)
