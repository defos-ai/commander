package commands

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
)

const (
	AdapterRequest         = messages.AdapterRequest
	AdapterRequestCanceled = messages.AdapterRequestCanceled
)

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

const (
	MethodDiscoverActions     = "agents.discover"
	MethodRunAction           = "agents.run_action"
	MethodStopAgent           = "agents.stop"
	MethodStartOperation      = "operations.start"
	MethodResolveContinuation = "operations.resolve_continuation"
	MethodAssignTask          = "operations.assign"
	MethodAskQuestion         = "operations.ask"
	MethodAnswerQuestion      = "operations.answer"
	MethodProposeCompletion   = "operations.propose_completion"
	MethodCompleteOperation   = "operations.complete"
	MethodFailOperation       = "operations.fail"
	MethodCancelOperation     = "operations.cancel"
)
