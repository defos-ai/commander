package mappings

import (
	"bytes"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func ToResolveContinuationCommand(operationID, taskID string, request models.ResolveContinuationRequest) controllercommands.ResolveContinuationCommand {
	var nextActions []actions.NextAction
	if request.Actions != nil {
		nextActions = make([]actions.NextAction, len(request.Actions))
		for i, action := range request.Actions {
			nextActions[i] = actions.NextAction{
				ActionName: action.ActionName,
				Input:      bytes.Clone(action.Input),
				Reason:     action.Reason,
			}
		}
	}
	return controllercommands.ResolveContinuationCommand{
		OperationID: operationID,
		TaskID:      taskID,
		Version:     request.Version,
		Actions:     nextActions,
	}
}

func ToContinuationResponse(continuation operations.Continuation) models.ContinuationResponse {
	return models.ContinuationResponse{
		Version:         continuation.Version,
		DecisionBy:      string(continuation.DecisionBy),
		Status:          string(continuation.Status),
		ProposedActions: ToNextActionResponses(continuation.ProposedActions),
		CreatedTaskIDs:  slices.Clone(continuation.CreatedTaskIDs),
		QuestionID:      continuation.QuestionID,
	}
}

func ToNextActionResponse(action actions.NextAction) models.NextActionResponse {
	return models.NextActionResponse{
		ActionName: action.ActionName,
		Input:      bytes.Clone(action.Input),
		Reason:     action.Reason,
	}
}

func ToNextActionResponses(actions []actions.NextAction) []models.NextActionResponse {
	if actions == nil {
		return nil
	}
	responses := make([]models.NextActionResponse, len(actions))
	for i, action := range actions {
		responses[i] = ToNextActionResponse(action)
	}
	return responses
}
