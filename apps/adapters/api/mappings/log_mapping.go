package mappings

import (
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/executions"
)

func ToLogResponses(logs []executions.Log) []models.LogResponse {
	responses := make([]models.LogResponse, len(logs))
	for index, log := range logs {
		responses[index] = models.LogResponse{
			ID: log.ID, AgentID: log.AgentID, CommandID: log.CommandID, ActionName: log.ActionName,
			OperationID: log.Work.OperationID, TaskID: log.Work.TaskID, Message: log.Message, CreatedAt: log.CreatedAt,
		}
	}
	return responses
}
