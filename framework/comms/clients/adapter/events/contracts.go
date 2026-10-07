package events

import (
	"encoding/json"
	"errors"

	"github.com/defos-ai/commander/framework/comms/messages"
)

const ControllerResponse = messages.ControllerResponse

// ControllerReply correlates to the adapter request. Errors describe the result
// of the requested action; publishing this result completes request handling.
type ControllerReply struct {
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *ControllerError `json:"error,omitempty"`
}

func (r ControllerReply) Validate() error {
	if r.Error == nil {
		if !json.Valid(r.Result) {
			return errors.New("controller reply requires a JSON result")
		}
	} else if r.Error.Message == "" || len(r.Result) != 0 {
		return errors.New("controller reply requires either a result or an error")
	}
	return nil
}

type ControllerError struct {
	Message string          `json:"message"`
	Code    string          `json:"code,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}
