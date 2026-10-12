package mappings

import (
	"bytes"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
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

func ToOperationResponse(operation operations.Operation) models.OperationResponse {
	response := models.OperationResponse{
		ID:                 operation.ID,
		Mission:            ToMissionResponse(operation.Mission),
		Input:              bytes.Clone(operation.Input),
		Status:             string(operation.Status),
		Tasks:              ToTaskResponses(operation.Tasks),
		Questions:          ToQuestionResponses(operation.Questions),
		Output:             bytes.Clone(operation.Output),
		ReportID:           operation.ReportID,
		FinalizationTaskID: operation.FinalizationTaskID,
		Error:              operation.Error,
		Events:             ToEventResponses(operation.Events),
		CreatedAt:          operation.CreatedAt,
		UpdatedAt:          operation.UpdatedAt,
	}
	if operation.Preflight != nil {
		preflight := ToPreflightResponse(*operation.Preflight)
		response.Preflight = &preflight
	}
	return response
}

func ToOperationResponses(operations []operations.Operation) []models.OperationResponse {
	if operations == nil {
		return nil
	}
	responses := make([]models.OperationResponse, len(operations))
	for i, operation := range operations {
		responses[i] = ToOperationResponse(operation)
	}
	return responses
}
