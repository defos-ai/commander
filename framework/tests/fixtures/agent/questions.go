package fixtures

import (
	"context"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func StartQuestionAgent(t *testing.T, run func(context.Context) error) *SecretAgent {
	t.Helper()
	executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
		Action: actions.Action{Name: "question"},
		Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
			return struct{}{}, run(ctx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return StartCheckpointAgent(t, executor)
}

func PendingExecutionQuestion(t *testing.T, message messages.Message) operations.Question {
	t.Helper()
	request, err := controllerevents.DecodeQuestionRequest(message)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return operations.Question{ID: id.QuestionID(), Status: operations.QuestionPending,
		CommandID: request.CommandID, Key: request.Key, Text: request.Text, Options: request.Options,
		TaskID: message.Work.TaskID, StepID: message.Work.StepID, AskedAt: now.Add(-time.Minute), FallbackAt: now.Add(time.Minute)}
}

func QuestionCommand(t *testing.T, agentID string, deadline time.Time) messages.Message {
	t.Helper()
	command, err := agentcommands.NewRunAction(agentID, actions.ActionCall{ActionName: "question",
		Work: messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}})
	if err != nil {
		t.Fatal(err)
	}
	command.ExpiresAt = deadline
	return command
}
