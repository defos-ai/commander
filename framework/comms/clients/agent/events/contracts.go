package events

import (
	"fmt"

	"commander/framework/comms/messages"
	"commander/framework/id"
)

const ActionResultAccepted = messages.ActionResultAccepted

// ResultReceipt confirms that the controller saved an action outcome durably.
// Its envelope correlates to the original command and carries the same work.
type ResultReceipt struct {
	ResultID string `json:"result_id"`
}

func (r ResultReceipt) Validate() error {
	if !id.Valid(r.ResultID) {
		return fmt.Errorf("result ID must be a UUID")
	}
	return nil
}

// NewResultAccepted creates a receipt for an accepted result. Publication belongs
// after durable result storage; constructing the receipt does not acknowledge it.
func NewResultAccepted(result messages.Message) (messages.Message, error) {
	if result.Type != messages.ActionSucceeded && result.Type != messages.CommandFailed {
		return messages.Message{}, fmt.Errorf("unsupported command result type %q", result.Type)
	}
	if err := result.Validate(); err != nil {
		return messages.Message{}, err
	}
	if !id.Valid(result.CorrelationID) {
		return messages.Message{}, fmt.Errorf("command result requires a command UUID")
	}
	receipt, err := messages.NewMessage(ActionResultAccepted, result.AgentID, ResultReceipt{ResultID: result.ID})
	if err != nil {
		return messages.Message{}, err
	}
	receipt.CorrelationID, receipt.Work = result.CorrelationID, result.Work
	return receipt, nil
}
