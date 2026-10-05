package mappings

import (
	"slices"

	"commander/adapters/api/models"
	"commander/framework/domain/operations"
)

func ToPreflightResponse(preflight operations.Preflight) models.PreflightResponse {
	response := models.PreflightResponse{Ready: preflight.Ready, Version: preflight.Version}
	if preflight.Actions != nil {
		response.Actions = make([]models.ActionReadinessResponse, len(preflight.Actions))
		for i, action := range preflight.Actions {
			mapped := models.ActionReadinessResponse{
				ActionName:    action.ActionName,
				ReadyAgentIDs: slices.Clone(action.ReadyAgentIDs),
			}
			if action.UnavailableAgents != nil {
				mapped.UnavailableAgents = make([]models.AgentReadinessResponse, len(action.UnavailableAgents))
				for j, agent := range action.UnavailableAgents {
					mapped.UnavailableAgents[j] = models.AgentReadinessResponse{
						AgentID:             agent.AgentID,
						Reason:              agent.Reason,
						MissingRequirements: ToActionRequirementResponses(agent.MissingRequirements),
					}
				}
			}
			response.Actions[i] = mapped
		}
	}
	return response
}
