package action

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/defos-ai/commander/framework/domain/operations"
)

var (
	ErrQuestionsUnavailable = errors.New("questions require an active controller-dispatched operation execution")
	ErrQuestionRejected     = errors.New("execution question was rejected")
	ErrQuestionConflict     = errors.New("execution question key already belongs to different content")
)

type QuestionProvider interface {
	Question(context.Context, string, string, []string) (operations.Question, error)
}

type questionContextKey struct{}
type questionContext struct{ provider QuestionProvider }

func WithQuestions(ctx context.Context, provider QuestionProvider) context.Context {
	return context.WithValue(ctx, questionContextKey{}, questionContext{provider})
}

// Question waits for a saved answer. The key is stable within one execution;
// repeating it must use the same text and options. Questions contain no secrets.
func Question(ctx context.Context, key, text string, options ...string) (operations.Question, error) {
	if err := ctx.Err(); err != nil {
		return operations.Question{}, err
	}
	if err := operations.ValidateExecutionQuestion(key, text, options); err != nil {
		return operations.Question{}, err
	}
	scoped, _ := ctx.Value(questionContextKey{}).(questionContext)
	if scoped.provider == nil {
		return operations.Question{}, ErrQuestionsUnavailable
	}
	question, err := scoped.provider.Question(ctx, key, text, slices.Clone(options))
	if err != nil {
		return operations.Question{}, err
	}
	if err := ctx.Err(); err != nil {
		return operations.Question{}, err
	}
	if question.Status != operations.QuestionAnswered || strings.TrimSpace(question.Answer) == "" || question.Key != key || question.Text != text ||
		!slices.Equal(question.Options, options) || question.AnsweredBy.Validate() != nil {
		return operations.Question{}, ErrQuestionRejected
	}
	return question.Clone(), nil
}
