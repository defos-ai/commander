package mappings

import (
	"bytes"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func ToAgentResponse(agent agents.Agent) models.AgentResponse {
	response := models.AgentResponse{
		ID:                 agent.ID,
		Type:               string(agent.Type),
		Status:             string(agent.Status),
		Server:             models.AgentServerResponse{IPAddress: agent.Server.IPAddress, Hostname: agent.Server.Hostname},
		Responsibilities:   slices.Clone(agent.Responsibilities),
		Actions:            ToActionResponses(agent.Actions),
		GrantedPermissions: slices.Clone(agent.GrantedPermissions),
		Config:             bytes.Clone(agent.Config),
		ObservedAt:         agent.ObservedAt,
		LastSeen:           agent.LastSeen,
		LastHeartbeat:      agent.LastHeartbeat,
		LeaseExpiresAt:     agent.LeaseExpiresAt,
		Stale:              agent.Stale,
	}
	if agent.Work != nil {
		response.Work = &models.AgentWorkResponse{
			OperationID: agent.Work.OperationID,
			TaskID:      agent.Work.TaskID,
			StepID:      agent.Work.StepID,
			ActionName:  agent.Work.ActionName,
			CommandID:   agent.Work.CommandID,
		}
	}
	if agent.DiscoveryScope != nil {
		response.DiscoveryScope = &models.AgentDiscoveryScopeResponse{
			Paths:    slices.Clone(agent.DiscoveryScope.Paths),
			Services: slices.Clone(agent.DiscoveryScope.Services),
		}
	}
	return response
}

func ToAgentResponses(agents []agents.Agent) []models.AgentResponse {
	if agents == nil {
		return nil
	}
	responses := make([]models.AgentResponse, len(agents))
	for i, agent := range agents {
		responses[i] = ToAgentResponse(agent)
	}
	return responses
}
