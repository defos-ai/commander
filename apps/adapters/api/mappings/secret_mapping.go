package mappings

import (
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

func ToSecretResponse(metadata secrets.Metadata, now time.Time) models.SecretResponse {
	response := models.SecretResponse{
		ID: metadata.ID, AgentType: metadata.Key.Scope.AgentType,
		Profile: metadata.Key.Scope.Profile, Name: metadata.Key.Name,
		Version: metadata.Version, CreatedAt: metadata.CreatedAt, UpdatedAt: metadata.UpdatedAt,
		Status: string(secrets.AvailabilityAt(&metadata, now)),
	}
	if !metadata.ExpiresAt.IsZero() {
		expiresAt := metadata.ExpiresAt
		response.ExpiresAt = &expiresAt
	}
	return response
}

func ToSecretResponses(items []secrets.Metadata, now time.Time) []models.SecretResponse {
	responses := make([]models.SecretResponse, len(items))
	for i, item := range items {
		responses[i] = ToSecretResponse(item, now)
	}
	return responses
}
