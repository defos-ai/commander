package consumers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

type QuestionConsumer struct {
	Executions executions.ExecutionStore
	Operations operations.OperationStore
	Bus        comms.Bus
}

func (c QuestionConsumer) Consume(ctx context.Context, message messages.Message) error {
	request, err := controllerevents.DecodeQuestionRequest(message)
	if err != nil {
		return InvalidMessage(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	question, code, err := c.question(ctx, message, request)
	if err != nil {
		return err
	}
	response := agentevents.QuestionResponse{CommandID: request.CommandID, Key: request.Key, Error: code}
	if code == "" {
		response.Question = &question
	}
	reply, err := agentevents.NewQuestionResponse(message, response)
	if err != nil {
		return err
	}
	return (agentevents.Client{Bus: c.Bus}).Publish(ctx, reply)
}

func (c QuestionConsumer) question(ctx context.Context, message messages.Message, request controllerevents.QuestionRequest) (operations.Question, string, error) {
	record, err := c.Executions.Get(ctx, message.AgentID, request.CommandID)
	if errors.Is(err, executions.ErrUnknownExecution) {
		return operations.Question{}, agentevents.QuestionInactive, nil
	}
	if err != nil {
		return operations.Question{}, "", fmt.Errorf("read question execution: %w", err)
	}
	if err := checkpointOwner(record, message); err != nil {
		return operations.Question{}, "", err
	}
	if record.Result != nil || c.Operations == nil {
		return operations.Question{}, agentevents.QuestionInactive, nil
	}
	op, err := c.Operations.Get(ctx, message.Work.OperationID)
	if errors.Is(err, operations.ErrUnknownOperation) {
		return operations.Question{}, agentevents.QuestionInactive, nil
	}
	if err != nil {
		return operations.Question{}, "", fmt.Errorf("read question operation: %w", err)
	}
	if !questionExecutionActive(op, message, time.Now()) {
		return operations.Question{}, agentevents.QuestionInactive, nil
	}
	if saved, exists := findExecutionQuestion(op, request); exists {
		if !questionMatches(saved, message, request) {
			return operations.Question{}, agentevents.QuestionConflict, nil
		}
		if saved.Status == operations.QuestionPending {
			return saved, "", nil
		}
	}
	// A visible answer may come from an uncertain write. The identical keyed
	// save confirms persistence without changing its answer or waiting window.
	now := time.Now().UTC()
	question := operations.Question{ID: id.QuestionID(), Status: operations.QuestionPending,
		TaskID: message.Work.TaskID, StepID: message.Work.StepID, CommandID: request.CommandID, Key: request.Key,
		Text: request.Text, Options: slices.Clone(request.Options), AskedAt: now, FallbackAt: now.Add(operations.DefaultQuestionTimeout)}
	op, err = c.Operations.AskQuestion(ctx, op.ID, question, 0)
	if errors.Is(err, operations.ErrFinished) || errors.Is(err, operations.ErrUnknownTask) {
		return operations.Question{}, agentevents.QuestionInactive, nil
	}
	if errors.Is(err, operations.ErrOperationConflict) {
		return operations.Question{}, agentevents.QuestionConflict, nil
	}
	if err != nil {
		return operations.Question{}, "", fmt.Errorf("persist execution question: %w", err)
	}
	if !questionExecutionActive(op, message, time.Now()) {
		return operations.Question{}, agentevents.QuestionInactive, nil
	}
	if saved, exists := findExecutionQuestion(op, request); exists && questionMatches(saved, message, request) {
		return saved, "", nil
	}
	return operations.Question{}, "", errors.New("saved execution question is missing or changed")
}

func questionExecutionActive(op operations.Operation, message messages.Message, now time.Time) bool {
	if op.ID != message.Work.OperationID || op.Status.Terminal() {
		return false
	}
	for _, task := range op.Tasks {
		if task.ID == message.Work.TaskID {
			return task.Status == operations.TaskRunning && task.AgentID == message.AgentID && task.CommandID == message.CorrelationID &&
				task.StepID == message.Work.StepID && task.DeadlineAt != nil && now.Before(*task.DeadlineAt)
		}
	}
	return false
}

func findExecutionQuestion(op operations.Operation, request controllerevents.QuestionRequest) (operations.Question, bool) {
	for _, question := range op.Questions {
		if question.CommandID == request.CommandID && question.Key == request.Key {
			return question.Clone(), true
		}
	}
	return operations.Question{}, false
}

func questionMatches(question operations.Question, message messages.Message, request controllerevents.QuestionRequest) bool {
	return question.TaskID == message.Work.TaskID && question.StepID == message.Work.StepID && question.Text == request.Text && slices.Equal(question.Options, request.Options)
}
