package commands

import (
	"encoding/json"
	"fmt"
	"time"

	"commander/framework/comms/messages"
)

const (
	AdapterRequest         = messages.AdapterRequest
	AdapterRequestCanceled = messages.AdapterRequestCanceled
)

// CommandEnvelope identifies the command method, serialized payload, and deadline.
// The bus Message carries this envelope and supplies delivery metadata.
type CommandEnvelope struct {
	Method     string          `json:"method"`
	Parameters json.RawMessage `json:"parameters"`
	Deadline   time.Time       `json:"deadline"`
}

func (r CommandEnvelope) Validate() error {
	if r.Method == "" || r.Deadline.IsZero() || !json.Valid(r.Parameters) {
		return fmt.Errorf("controller command requires a method, JSON parameters and deadline")
	}
	return nil
}

// Method names identify controller commands accepted from adapters.
const (
	MethodDiscoverActions     = "agents.discover"
	MethodRunAction           = "agents.run_action"
	MethodStopAgent           = "agents.stop"
	MethodStartOperation      = "operations.start"
	MethodResolveContinuation = "operations.resolve_continuation"
	MethodAssignTask          = "operations.assign"
	MethodAskQuestion         = "operations.ask"
	MethodAnswerQuestion      = "operations.answer"
	MethodCompleteOperation   = "operations.complete"
	MethodFailOperation       = "operations.fail"
	MethodCancelOperation     = "operations.cancel"
)
