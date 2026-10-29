package fakes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
)

type QuestionFallbackCommander struct {
	mu        sync.Mutex
	operation bridge.Operation
	changes   chan struct{}
	reads     chan struct{}
	readCount atomic.Int32
	answers   atomic.Int32
}

func NewQuestionFallbackCommander(operation bridge.Operation) *QuestionFallbackCommander {
	return &QuestionFallbackCommander{operation: cloneFallbackOperation(operation), changes: make(chan struct{}, 1), reads: make(chan struct{}, 64)}
}

func (*QuestionFallbackCommander) Tools() []llm.Tool {
	return []llm.Tool{{Name: "answer_question", Schema: json.RawMessage(`{"type":"object"}`)}}
}
func (*QuestionFallbackCommander) Instructions() string     { return "" }
func (c *QuestionFallbackCommander) Reads() <-chan struct{} { return c.reads }
func (c *QuestionFallbackCommander) ReadCount() int32       { return c.readCount.Load() }
func (c *QuestionFallbackCommander) AnswerCount() int32     { return c.answers.Load() }

func (c *QuestionFallbackCommander) Operation(context.Context, string) (bridge.Operation, error) {
	c.mu.Lock()
	operation := cloneFallbackOperation(c.operation)
	c.mu.Unlock()
	c.readCount.Add(1)
	select {
	case c.reads <- struct{}{}:
	default:
	}
	return operation, nil
}

func (c *QuestionFallbackCommander) Call(ctx context.Context, name string, raw json.RawMessage) (bridge.Result, error) {
	if name == "operation_events" {
		var args struct {
			Wait int `json:"wait_seconds"`
		}
		if json.Unmarshal(raw, &args) != nil {
			return bridge.Result{}, errors.New("invalid event request")
		}
		if args.Wait != 0 {
			timer := time.NewTimer(50 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return bridge.Result{}, ctx.Err()
			case <-c.changes:
			case <-timer.C:
			}
		}
		return bridge.Result{Data: json.RawMessage(`{"events":[],"next_cursor":""}`)}, nil
	}
	if name != "answer_question" {
		return bridge.Result{}, fmt.Errorf("unexpected fallback tool %s", name)
	}
	c.answers.Add(1)
	var args struct {
		OperationID string `json:"operation_id"`
		QuestionID  string `json:"question_id"`
		Answer      string `json:"answer"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return bridge.Result{}, errors.New("invalid answer request")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	question := &c.operation.Questions[0]
	if args.OperationID != c.operation.ID || args.QuestionID != question.ID || !question.Pending() || time.Now().Before(question.FallbackAt) {
		return bridge.Result{IsError: true, Text: "question is not eligible for a model answer"}, nil
	}
	c.saveAnswer(args.Answer, "model")
	data := cloneFallbackOperation(c.operation).Raw
	return bridge.Result{Data: data, Text: string(data)}, nil
}

func (c *QuestionFallbackCommander) OperatorAnswer(answer string) bridge.Operation {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.saveAnswer(answer, "operator")
	return cloneFallbackOperation(c.operation)
}

func (c *QuestionFallbackCommander) saveAnswer(answer, origin string) {
	question := &c.operation.Questions[0]
	question.Status, question.Answer, question.AnsweredBy = bridge.QuestionAnswered, answer, origin
	c.operation.Status, c.operation.Progress.PendingQuestions = "planning", 0
	c.operation.UpdatedAt = time.Now().UTC()
	select {
	case c.changes <- struct{}{}:
	default:
	}
}

func cloneFallbackOperation(operation bridge.Operation) bridge.Operation {
	data, err := json.Marshal(operation)
	if err != nil {
		panic(err)
	}
	var copy bridge.Operation
	if err := json.Unmarshal(data, &copy); err != nil {
		panic(err)
	}
	copy.Raw = data
	return copy
}

type FallbackReview struct {
	AnswerAvailable bool
	StartsTurn      bool
}

type QuestionFallbackModel struct {
	operationID, questionID string
	reviews                 chan FallbackReview
	release                 chan struct{}
	hold                    bool
	first                   atomic.Bool
	eligible                atomic.Int32
}

func NewQuestionFallbackModel(operationID, questionID string, holdFirst bool) *QuestionFallbackModel {
	return &QuestionFallbackModel{operationID: operationID, questionID: questionID, reviews: make(chan FallbackReview, 32), release: make(chan struct{}), hold: holdFirst}
}
func (m *QuestionFallbackModel) Reviews() <-chan FallbackReview { return m.reviews }
func (m *QuestionFallbackModel) Release()                       { close(m.release) }
func (m *QuestionFallbackModel) EligibleReviews() int32         { return m.eligible.Load() }

func (m *QuestionFallbackModel) Complete(ctx context.Context, _ string, history []llm.Message, tools []llm.Tool, _ func(string)) (llm.Message, error) {
	available := false
	for _, tool := range tools {
		available = available || tool.Name == "answer_question"
	}
	last := history[len(history)-1]
	review := FallbackReview{AnswerAvailable: available, StartsTurn: last.Role == "user"}
	if review.AnswerAvailable && review.StartsTurn {
		m.eligible.Add(1)
	}
	select {
	case m.reviews <- review:
	case <-ctx.Done():
		return llm.Message{}, ctx.Err()
	}
	if m.hold && !m.first.Swap(true) {
		select {
		case <-m.release:
		case <-ctx.Done():
			return llm.Message{}, ctx.Err()
		}
	}
	if available && last.Role != "tool" {
		args, _ := json.Marshal(map[string]string{"operation_id": m.operationID, "question_id": m.questionID, "answer": "Continue with existing guidance"})
		return llm.Message{Role: "assistant", Calls: []llm.Call{{ID: "fallback-answer", Name: "answer_question", Arguments: args}}}, nil
	}
	return llm.Message{Role: "assistant", Text: "Question reviewed"}, nil
}
