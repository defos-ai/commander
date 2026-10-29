package mappings

import (
	"bytes"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/queries"
)

func ToAssignTaskCommand(operationID string, request models.AssignTaskRequest) controllercommands.AssignTaskCommand {
	return controllercommands.AssignTaskCommand{
		OperationID: operationID,
		ActionName:  request.ActionName,
		Input:       bytes.Clone(request.Input),
	}
}

func ToTaskResponse(task operations.Task) models.TaskResponse {
	response := models.TaskResponse{
		ID:           task.ID,
		OperationID:  task.OperationID,
		ParentTaskID: task.ParentTaskID,
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
	if task.SessionIntent != nil {
		i := task.SessionIntent
		response.SessionIntent = &models.SessionIntentResponse{Mode: string(i.Mode), ScopeID: i.ScopeID, ID: i.ID, Error: i.Error}
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

func ToTaskViewResponse(task queries.TaskView) models.TaskResponse {
	response := ToTaskResponse(task.Task)
	response.AgentType = task.AgentType
	response.MissingRequirements = ToActionRequirementResponses(task.MissingRequirements)
	return response
}

func ToTaskViewResponses(tasks []queries.TaskView) []models.TaskResponse {
	if tasks == nil {
		return nil
	}
	responses := make([]models.TaskResponse, len(tasks))
	for i, task := range tasks {
		responses[i] = ToTaskViewResponse(task)
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
