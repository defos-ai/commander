package fakes

import (
	"context"
	"slices"

	"github.com/defos-ai/commander/framework/domain/operations"
)

type SourceQuestionRequest struct {
	Key, Text string
	Options   []string
}

type sourceQuestionReply struct {
	question operations.Question
	err      error
}

type SourceQuestions struct {
	requests chan SourceQuestionRequest
	replies  chan sourceQuestionReply
}

func NewSourceQuestions() *SourceQuestions {
	return &SourceQuestions{requests: make(chan SourceQuestionRequest, 1), replies: make(chan sourceQuestionReply, 1)}
}

func (s *SourceQuestions) Question(ctx context.Context, key, text string, options []string) (operations.Question, error) {
	select {
	case s.requests <- SourceQuestionRequest{Key: key, Text: text, Options: slices.Clone(options)}:
	case <-ctx.Done():
		return operations.Question{}, ctx.Err()
	}
	select {
	case reply := <-s.replies:
		return reply.question.Clone(), reply.err
	case <-ctx.Done():
		return operations.Question{}, ctx.Err()
	}
}

func (s *SourceQuestions) Requests() <-chan SourceQuestionRequest { return s.requests }

func (s *SourceQuestions) Answer(question operations.Question, err error) {
	s.replies <- sourceQuestionReply{question: question.Clone(), err: err}
}
