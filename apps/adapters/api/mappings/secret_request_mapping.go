package mappings

import (
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

func ToSecretRequestResponse(request secrets.Request, status string) models.SecretRequestResponse {
	return models.SecretRequestResponse{
		ID: request.ID, Type: string(request.EffectiveType()), AgentType: request.AgentType, Name: request.Key.Name,
		AgentID: request.AgentID, OperationID: request.OperationID, TaskID: request.TaskID, CommandID: request.CommandID,
		RequestedAt: request.RequestedAt, ExpiresAt: request.ExpiresAt, Status: status,
	}
}
