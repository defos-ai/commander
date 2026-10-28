package mappings

import (
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func ToCatalogEntry(request models.CreateCatalogAgentRequest, now time.Time) agents.CatalogEntry {
	return agents.CatalogEntry{
		Name: request.Name, Description: request.Description, Package: request.Package,
		Visibility: packages.Visibility(request.Visibility), Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func ToCatalogAgentResponse(entry agents.CatalogEntry) models.CatalogAgentResponse {
	return models.CatalogAgentResponse{
		Name: entry.Name, Description: entry.Description, Package: entry.Package,
		Visibility: string(entry.Visibility), Trusted: entry.Trusted, Version: entry.Version,
		CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt,
		TrustUpdatedBy: entry.TrustUpdatedBy, TrustUpdatedAt: entry.TrustUpdatedAt,
	}
}

func ToCatalogAgentResponses(entries []agents.CatalogEntry) []models.CatalogAgentResponse {
	responses := make([]models.CatalogAgentResponse, len(entries))
	for index, entry := range entries {
		responses[index] = ToCatalogAgentResponse(entry)
	}
	return responses
}
