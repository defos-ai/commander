package mappings

import (
	"bytes"

	"commander/adapters/api/models"
	controllercommands "commander/framework/comms/clients/controller/commands"
)

func ToStartOperationCommand(request models.StartOperationRequest) controllercommands.StartOperationCommand {
	return controllercommands.StartOperationCommand{
		OperationID: request.OperationID,
		MissionName: request.MissionName,
		Input:       bytes.Clone(request.Input),
	}
}

func ToCancelOperationCommand(operationID string) controllercommands.CancelOperationCommand {
	return controllercommands.CancelOperationCommand{OperationID: operationID}
}

func ToCompleteOperationCommand(operationID string, request models.CompleteOperationRequest) controllercommands.CompleteOperationCommand {
	return controllercommands.CompleteOperationCommand{
		OperationID: operationID,
		Output:      bytes.Clone(request.Output),
	}
}

func ToFailOperationCommand(operationID string, request models.FailOperationRequest) controllercommands.FailOperationCommand {
	return controllercommands.FailOperationCommand{
		OperationID: operationID,
		Reason:      request.Reason,
	}
}
