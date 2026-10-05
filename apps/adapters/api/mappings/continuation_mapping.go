package mappings

import (
	"bytes"

	"commander/adapters/api/models"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/actions"
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
