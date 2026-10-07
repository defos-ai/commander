package mappings

import (
	"bytes"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func ToAssignTaskCommand(operationID string, request models.AssignTaskRequest) controllercommands.AssignTaskCommand {
	return controllercommands.AssignTaskCommand{
		OperationID: operationID,
		StepID:      request.StepID,
		ActionName:  request.ActionName,
		Input:       bytes.Clone(request.Input),
	}
}

func ToTaskResponse(task operations.Task) models.TaskResponse {
	response := models.TaskResponse{
		ID:           task.ID,
		OperationID:  task.OperationID,
		ParentTaskID: task.ParentTaskID,
		StepID:       task.StepID,
		AgentID:      task.AgentID,
		ActionName:   task.ActionName,
		CommandID:    task.CommandID,
		DeadlineAt:   cloneTime(task.DeadlineAt),
		Input:        bytes.Clone(task.Input),
		Status:       string(task.Status),
		Error:        task.Error,
		StartedAt:    task.StartedAt,
		FinishedAt:   cloneTime(task.FinishedAt),
	}
	if task.Result != nil {
		result := ToActionOutputResponse(*task.Result)
		response.Result = &result
	}
	if task.Continuation != nil {
		continuation := ToContinuationResponse(*task.Continuation)
		response.Continuation = &continuation
	}
	return response
}

func ToTaskResponses(tasks []operations.Task) []models.TaskResponse {
	if tasks == nil {
		return nil
	}
	responses := make([]models.TaskResponse, len(tasks))
	for i, task := range tasks {
		responses[i] = ToTaskResponse(task)
	}
	return responses
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
