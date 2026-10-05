package mappings

import (
	"bytes"

	"commander/adapters/api/models"
	controllercommands "commander/framework/comms/clients/controller/commands"
)

func ToAssignTaskCommand(operationID string, request models.AssignTaskRequest) controllercommands.AssignTaskCommand {
	return controllercommands.AssignTaskCommand{
		OperationID: operationID,
		StepID:      request.StepID,
		ActionName:  request.ActionName,
		Input:       bytes.Clone(request.Input),
	}
}
