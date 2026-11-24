package mappings

import (
	"bytes"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func ToAgentResponse(agent agents.Agent) models.AgentResponse {
	response := models.AgentResponse{
		ID:               agent.ID,
		Sessions:         ToRuntimeSessionResponses(agent.Sessions),
		Type:             string(agent.Type),
		Version:          agent.Version,
		Status:           string(agent.Status),
		StopCommandID:    agent.StopCommandID,
		StopRequestedAt:  agent.StopRequestedAt,
		Server:           models.AgentServerResponse{IPAddress: agent.Server.IPAddress, Hostname: agent.Server.Hostname},
		Responsibilities: slices.Clone(agent.Responsibilities),
		Actions:          ToActionResponses(agent.Actions),
		Config:           bytes.Clone(agent.Config),
		ObservedAt:       agent.ObservedAt,
		LastSeen:         agent.LastSeen,
		LastHeartbeat:    agent.LastHeartbeat,
		LeaseExpiresAt:   agent.LeaseExpiresAt,
		Stale:            agent.Stale,
	}
	for _, dependency := range agent.Dependencies {
		response.Dependencies = append(response.Dependencies, models.AgentDependencyResponse{
			Name: dependency.Name, Description: dependency.Description, Version: dependency.Version,
		})
	}
	if agent.Work != nil {
		response.Work = &models.AgentWorkResponse{
			OperationID:         agent.Work.OperationID,
			TaskID:              agent.Work.TaskID,
			ActionName:          agent.Work.ActionName,
			CommandID:           agent.Work.CommandID,
			MissingRequirements: ToActionRequirementResponses(agent.Work.MissingRequirements),
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

func ToAgentSelector(request models.AgentSelectorRequest) agents.Selector {
	return agents.Selector{ID: request.ID, Type: agents.AgentType(request.Type), Version: request.Version, Hostname: request.Hostname}
}

func ToStopAgentsResponse(result agents.StopResult) models.StopAgentsResponse {
	response := models.StopAgentsResponse{Items: ToAgentResponses(result.Agents)}
	for _, failure := range result.Errors {
		response.Errors = append(response.Errors, models.AgentStopErrorResponse{ID: failure.ID, Error: failure.Error})
	}
	return response
}
