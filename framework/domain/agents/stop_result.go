package agents

import (
	"errors"

	"github.com/defos-ai/commander/framework/id"
)

type StopResult struct {
	Agents []Agent     `json:"agents"`
	Errors []StopError `json:"errors,omitempty"`
}

type StopError struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

func (result StopResult) Validate() error {
	if result.Agents == nil {
		return errors.New("agent stop result requires an agents array")
	}
	seen := make(map[string]bool)
	for _, agent := range result.Agents {
		if !id.ValidAgent(agent.ID) || seen[agent.ID] || agent.Status != AgentStopRequested && agent.Status != AgentStopped {
			return errors.New("invalid or duplicate stopped agent result")
		}
		if agent.Status == AgentStopRequested || agent.StopCommandID != "" || !agent.StopRequestedAt.IsZero() {
			if err := ValidateStopRequest(agent.StopCommandID, agent.StopRequestedAt); err != nil {
				return err
			}
		}
		seen[agent.ID] = true
	}
	for _, failure := range result.Errors {
		if !id.ValidAgent(failure.ID) || seen[failure.ID] {
			return errors.New("invalid or duplicate agent stop failure")
		}
		switch failure.Error {
		case "agent_not_found", "agent_stop_uncertain", "agent_stop_not_requested":
		default:
			return errors.New("invalid agent stop failure code")
		}
		seen[failure.ID] = true
	}
	return nil
}
