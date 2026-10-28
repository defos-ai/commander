package mappings

import (
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func ToAgentSource(request models.CreateAgentSourceRequest, now time.Time) agents.AgentSource {
	return agents.AgentSource{
		Name: request.Name, Description: request.Description, Source: request.Source,
		Visibility: packages.Visibility(request.Visibility), Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func ToAgentSourceResponse(entry agents.AgentSource) models.AgentSourceResponse {
	return models.AgentSourceResponse{
		Name: entry.Name, Description: entry.Description, Source: entry.Source,
		Visibility: string(entry.Visibility), Trusted: entry.Trusted, Version: entry.Version,
		CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt,
		TrustUpdatedBy: entry.TrustUpdatedBy, TrustUpdatedAt: entry.TrustUpdatedAt,
	}
}

func ToAgentSourceResponses(entries []agents.AgentSource) []models.AgentSourceResponse {
	responses := make([]models.AgentSourceResponse, len(entries))
	for index, entry := range entries {
		responses[index] = ToAgentSourceResponse(entry)
	}
	return responses
}
