package mappings

import (
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func ToEventResponse(event operations.Event) models.EventResponse {
	return models.EventResponse{
		Sequence:    event.Sequence,
		OperationID: event.OperationID,
		Type:        string(event.Type),
		Status:      string(event.Status),
		TaskID:      event.TaskID,
		QuestionID:  event.QuestionID,
		Message:     event.Message,
		CreatedAt:   event.CreatedAt,
	}
}

func ToEventResponses(events []operations.Event) []models.EventResponse {
	if events == nil {
		return nil
	}
	responses := make([]models.EventResponse, len(events))
	for i, event := range events {
		responses[i] = ToEventResponse(event)
	}
	return responses
}
