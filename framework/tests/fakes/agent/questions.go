package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/operations"
)

type QuestionProvider func(context.Context, string, string, []string) (operations.Question, error)

func (f QuestionProvider) Question(ctx context.Context, key, text string, options []string) (operations.Question, error) {
	return f(ctx, key, text, options)
}
