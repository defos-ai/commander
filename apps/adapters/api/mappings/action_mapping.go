package mappings

import (
	"bytes"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/actions"
)

func ToActionResponse(action actions.Action) models.ActionResponse {
	return models.ActionResponse{
		Name:                action.Name,
		Description:         action.Description,
		StatusReason:        action.StatusReason,
		Status:              string(action.Status),
		MissingRequirements: ToActionRequirementResponses(action.MissingRequirements),
		InputSchema:         bytes.Clone(action.InputSchema),
		OutputSchema:        bytes.Clone(action.OutputSchema),
	}
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

func ToActionRequirementResponses(requirements []actions.Requirement) []models.ActionRequirementResponse {
	if requirements == nil {
		return nil
	}
	responses := make([]models.ActionRequirementResponse, len(requirements))
	for i, requirement := range requirements {
		responses[i] = models.ActionRequirementResponse{
			Kind:   string(requirement.Kind),
			Name:   requirement.Name,
			Secret: requirement.Secret,
		}
	}
	return responses
}

func ToActionOutputResponse(output actions.ActionOutput) models.ActionOutputResponse {
	return models.ActionOutputResponse{
		ActionName:  output.ActionName,
		Output:      bytes.Clone(output.Output),
		Artifacts:   ToArtifactResponses(output.Artifacts),
		NextActions: ToNextActionResponses(output.NextActions),
	}
}
