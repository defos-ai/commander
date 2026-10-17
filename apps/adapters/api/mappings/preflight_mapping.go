package mappings

import (
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/operations"
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
						AgentType:           agent.AgentType,
						Reason:              agent.Reason,
						MissingRequirements: ToActionRequirementResponses(agent.MissingRequirements),
						MissingSecrets:      slices.Clone(agent.MissingSecrets),
					}
				}
			}
			response.Actions[i] = mapped
		}
	}
	return response
}
