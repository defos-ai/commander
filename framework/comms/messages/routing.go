package messages

import (
	"errors"

	"commander/framework/id"
)

const DefaultControllerType = "default"

func AgentCommands(agentID string) string { return "agents." + agentID + ".commands" }

// AgentEvents is consumed independently so long-running commands cannot delay
// confirmation that earlier action outcomes have reached the controller database.
func AgentEvents(agentID string) string { return "agents." + agentID + ".events" }

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

func ControllerCommands(controllerType string) string {
	return "controllers." + controllerType + ".commands"
}
func ControllerCancellations(controllerType string) string {
	return "controllers." + controllerType + ".cancellations"
}

func AdapterReplies(adapterID string) string { return "adapters." + adapterID + ".replies" }
