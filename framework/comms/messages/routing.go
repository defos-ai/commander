package messages

import (
	"errors"

	"commander/framework/id"
)

const DefaultControllerType = "default"

// AgentEvents is the default controller type's event topic. Use ControllerEvents for
// independently routed controller types sharing the same transport.
const AgentEvents = "controllers." + DefaultControllerType + ".events"

func AgentCommands(agentID string) string { return "agents." + agentID + ".commands" }

// AgentReceipts is consumed independently so long-running commands cannot delay
// confirmation that earlier action outcomes have reached the controller database.
func AgentReceipts(agentID string) string { return "agents." + agentID + ".receipts" }

func ControllerEvents(controllerType string) string {
	return "controllers." + controllerType + ".events"
}

// ValidateControllerType accepts a stable lowercase routing destination.
func ValidateControllerType(controllerType string) error {
	if !id.ValidType(controllerType) {
		return errors.New("controller type must be a lowercase type slug")
	}
	return nil
}

func ControllerRequests(controllerType string) string {
	return "controllers." + controllerType + ".requests"
}
func ControllerCancellations(controllerType string) string {
	return "controllers." + controllerType + ".cancellations"
}

func AdapterReplies(adapterID string) string { return "adapters." + adapterID + ".replies" }
