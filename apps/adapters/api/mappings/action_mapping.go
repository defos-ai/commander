package mappings

import (
	"bytes"
	"slices"

	"commander/adapters/api/models"
	"commander/framework/domain/actions"
)

func ToActionResponse(action actions.Action) models.ActionResponse {
	response := models.ActionResponse{
		Name:                action.Name,
		Description:         action.Description,
		StatusReason:        action.StatusReason,
		Status:              string(action.Status),
		RequiredPermissions: slices.Clone(action.RequiredPermissions),
		InputSchema:         bytes.Clone(action.InputSchema),
		OutputSchema:        bytes.Clone(action.OutputSchema),
	}
	if action.MissingRequirements != nil {
		response.MissingRequirements = make([]models.ActionRequirementResponse, len(action.MissingRequirements))
		for i, requirement := range action.MissingRequirements {
			response.MissingRequirements[i] = models.ActionRequirementResponse{
				Kind:   string(requirement.Kind),
				Name:   requirement.Name,
				Secret: requirement.Secret,
			}
		}
	}
	return response
}

func ToActionResponses(actions []actions.Action) []models.ActionResponse {
	if actions == nil {
		return nil
	}
	responses := make([]models.ActionResponse, len(actions))
	for i, action := range actions {
		responses[i] = ToActionResponse(action)
	}
	return responses
}
