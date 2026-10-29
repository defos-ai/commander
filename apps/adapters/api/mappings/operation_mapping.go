package mappings

import (
	"bytes"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/queries"
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
		CompletedBy: operations.CompletionOperator,
	}
}

func ToProposeCompletionCommand(operationID string, request models.ProposeCompletionRequest) controllercommands.ProposeCompletionCommand {
	return controllercommands.ProposeCompletionCommand{
		OperationID: operationID,
		Output:      bytes.Clone(request.Output),
		ProposedBy:  operations.CompletionOperator,
	}
}

func ToFailOperationCommand(operationID string, request models.FailOperationRequest) controllercommands.FailOperationCommand {
	return controllercommands.FailOperationCommand{
		OperationID: operationID,
		Reason:      request.Reason,
	}
}

func ToOperationResponse(operation operations.Operation) models.OperationResponse {
	return toOperationResponse(operation, ToTaskResponses(operation.Tasks), operation.AssessAffinity(), operation.AssessProgress(time.Now().UTC()))
}

func ToOperationViewResponse(operation queries.OperationView) models.OperationResponse {
	return toOperationResponse(operation.Operation, ToTaskViewResponses(operation.Tasks), operation.Affinity, operation.Progress)
}

func toOperationResponse(operation operations.Operation, tasks []models.TaskResponse, affinity *operations.Affinity, progress operations.Progress) models.OperationResponse {
	response := models.OperationResponse{
		ID:          operation.ID,
		Mission:     ToMissionResponse(operation.Mission),
		Input:       bytes.Clone(operation.Input),
		Status:      string(operation.Status),
		Progress:    toOperationProgressResponse(progress),
		Tasks:       tasks,
		Questions:   ToQuestionResponses(operation.Questions),
		Output:      bytes.Clone(operation.Output),
		ReportID:    operation.ReportID,
		CompletedBy: string(operation.CompletedBy),
		Error:       operation.Error,
		Events:      ToEventResponses(operation.Events),
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Preflight != nil {
		preflight := ToPreflightResponse(*operation.Preflight)
		response.Preflight = &preflight
	}
	if proposal := operation.CompletionProposal; proposal != nil {
		response.CompletionProposal = &models.CompletionProposalResponse{
			ID: proposal.ID, Output: bytes.Clone(proposal.Output), ProposedBy: string(proposal.ProposedBy), ProposedAt: proposal.ProposedAt,
		}
	}
	if affinity != nil {
		response.Affinity = &models.AffinityResponse{
			ForActions: affinity.ForActions,
			AgentID:    affinity.AgentID,
			Error:      affinity.Error,
		}
	}
	return response
}

func toOperationProgressResponse(progress operations.Progress) models.OperationProgressResponse {
	counts := make(map[string]int, len(progress.TaskCounts))
	for status, count := range progress.TaskCounts {
		counts[string(status)] = count
	}
	return models.OperationProgressResponse{
		TaskCounts: counts, TotalTasks: progress.TotalTasks, FinishedTasks: progress.FinishedTasks,
		PendingDecisions: progress.PendingDecisions, PendingQuestions: progress.PendingQuestions,
		Settled: progress.Settled,
	}
}

func ToOperationViewResponses(operations []queries.OperationView) []models.OperationResponse {
	if operations == nil {
		return nil
	}
	responses := make([]models.OperationResponse, len(operations))
	for i, operation := range operations {
		responses[i] = ToOperationViewResponse(operation)
	}
	return responses
}
