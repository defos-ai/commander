package mappings

import (
	"commander/adapters/api/models"
	"commander/framework/domain/controllers"
)

func ToControllerResponse(controller controllers.Controller) models.ControllerResponse {
	return models.ControllerResponse{
		ID:             controller.ID,
		Type:           string(controller.Type),
		Status:         string(controller.Status),
		StatusReason:   controller.StatusReason,
		Server:         models.ControllerServerResponse{Hostname: controller.Server.Hostname, OS: controller.Server.OS, Arch: controller.Server.Arch},
		Version:        controller.Version,
		AgentSelector:  controller.AgentSelector,
		StartedAt:      controller.StartedAt,
		LastHeartbeat:  controller.LastHeartbeat,
		LeaseExpiresAt: controller.LeaseExpiresAt,
		Stale:          controller.Stale,
	}
}

func ToControllerResponses(controllers []controllers.Controller) []models.ControllerResponse {
	if controllers == nil {
		return nil
	}
	responses := make([]models.ControllerResponse, len(controllers))
	for i, controller := range controllers {
		responses[i] = ToControllerResponse(controller)
	}
	return responses
}
