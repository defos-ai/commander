package mappings

import (
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func ToAskQuestionCommand(operationID string, request models.AskQuestionRequest) controllercommands.AskQuestionCommand {
	return controllercommands.AskQuestionCommand{
		OperationID:    operationID,
		TaskID:         request.TaskID,
		Version:        request.Version,
		StepID:         request.StepID,
		Text:           request.Text,
		Options:        slices.Clone(request.Options),
		TimeoutSeconds: request.TimeoutSeconds,
	}
}

func ToAnswerQuestionCommand(operationID, questionID string, request models.AnswerQuestionRequest) controllercommands.AnswerQuestionCommand {
	return controllercommands.AnswerQuestionCommand{
		OperationID: operationID,
		QuestionID:  questionID,
		Answer:      request.Answer,
		AnsweredBy:  operations.AnswerOperator,
	}
}

func ToQuestionResponse(question operations.Question) models.QuestionResponse {
	return models.QuestionResponse{
		ID:         question.ID,
		Status:     string(question.Status),
		StepID:     question.StepID,
		TaskID:     question.TaskID,
		Text:       question.Text,
		Options:    slices.Clone(question.Options),
		Answer:     question.Answer,
		AnsweredBy: string(question.AnsweredBy),
		AskedAt:    question.AskedAt,
		AnsweredAt: cloneTime(question.AnsweredAt),
		ExpiresAt:  question.ExpiresAt,
	}
}

func ToQuestionResponses(questions []operations.Question) []models.QuestionResponse {
	if questions == nil {
		return nil
	}
	responses := make([]models.QuestionResponse, len(questions))
	for i, question := range questions {
		responses[i] = ToQuestionResponse(question)
	}
	return responses
}
