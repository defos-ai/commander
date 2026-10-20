package mappings

import (
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

func ToSecretRequestResponse(request secrets.Request, status string) models.SecretRequestResponse {
	return models.SecretRequestResponse{
		ID: request.ID, AgentType: request.Key.Scope.AgentType, Name: request.Key.Name,
		AgentID:     request.AgentID,
		RequestedAt: request.RequestedAt, ExpiresAt: request.ExpiresAt, Status: status,
	}
}
