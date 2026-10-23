package mappings

import (
	"bytes"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/actions"
)

func ToActionDefinitions(requests []models.ActionDefinitionRequest) []actions.Definition {
	if requests == nil {
		return nil
	}
	definitions := make([]actions.Definition, len(requests))
	for i, request := range requests {
		definitions[i] = actions.Definition{
			Name:            request.Name,
			Description:     request.Description,
			RequiredSecrets: slices.Clone(request.RequiredSecrets),
			InputSchema:     bytes.Clone(request.InputSchema),
			OutputSchema:    bytes.Clone(request.OutputSchema),
		}
	}
	return definitions
}

func ToActionDefinitionResponses(definitions []actions.Definition) []models.ActionDefinitionResponse {
	if definitions == nil {
		return nil
	}
	responses := make([]models.ActionDefinitionResponse, len(definitions))
	for i, definition := range definitions {
		responses[i] = models.ActionDefinitionResponse{
			Name:            definition.Name,
			Description:     definition.Description,
			RequiredSecrets: slices.Clone(definition.RequiredSecrets),
			InputSchema:     bytes.Clone(definition.InputSchema),
			OutputSchema:    bytes.Clone(definition.OutputSchema),
		}
	}
	return responses
}

func ToActionResponse(action actions.Action) models.ActionResponse {
	return models.ActionResponse{
		Name:                action.Name,
		SessionMode:         action.SessionMode,
		Description:         action.Description,
		StatusReason:        action.StatusReason,
		Status:              string(action.Status),
		MissingRequirements: ToActionRequirementResponses(action.MissingRequirements),
		RequiredSecrets:     slices.Clone(action.RequiredSecrets),
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
			Kind:      string(requirement.Kind),
			Name:      requirement.Name,
			Sensitive: requirement.Sensitive,
		}
	}
	return responses
}

func ToActionOutputResponse(output actions.ActionOutput) models.ActionOutputResponse {
	var session *models.RuntimeSessionResponse
	if output.Session != nil {
		v := ToRuntimeSessionResponse(*output.Session)
		session = &v
	}
	return models.ActionOutputResponse{
		Session:     session,
		ActionName:  output.ActionName,
		Output:      bytes.Clone(output.Output),
		Artifacts:   ToArtifactResponses(output.Artifacts),
		NextActions: ToNextActionResponses(output.NextActions),
	}
}
