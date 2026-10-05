package mappings

import (
	"slices"

	"commander/adapters/api/models"
	controllercommands "commander/framework/comms/clients/controller/commands"
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
	}
}
