package fakes

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type QuestionWindowStore struct {
	operations.OperationStore
	Window time.Duration
}

func (s QuestionWindowStore) AskQuestion(ctx context.Context, operationID string, question operations.Question, version uint64) (operations.Operation, error) {
	if question.CommandID != "" {
		question.FallbackAt = question.AskedAt.Add(s.Window)
	}
	return s.OperationStore.AskQuestion(ctx, operationID, question, version)
}

type QuestionAction struct {
	*action.Base
	invocations atomic.Int32
}

func NewQuestionAction() *QuestionAction {
	return &QuestionAction{Base: action.NewBase(actions.Action{Name: "confirm-action", Description: "Wait for an execution question",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)})}
}

func (a *QuestionAction) Invocations() int32 { return a.invocations.Load() }

func (a *QuestionAction) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	a.invocations.Add(1)
	question, err := action.Question(ctx, "confirm-action", "Continue this execution?", "Yes", "No")
	if err != nil {
		return action.Result{}, err
	}
	repeated, err := action.Question(ctx, "confirm-action", "Continue this execution?", "Yes", "No")
	if err != nil {
		return action.Result{}, err
	}
	return action.JSONResult(map[string]string{"question_id": question.ID, "repeated_question_id": repeated.ID,
		"answer": question.Answer, "answered_by": string(question.AnsweredBy)})
}
