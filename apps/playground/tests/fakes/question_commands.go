package fakes

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
)

type QuestionCommands struct {
	mu               sync.Mutex
	pending          json.RawMessage
	answered         json.RawMessage
	arguments        json.RawMessage
	receipt          json.RawMessage
	failRefresh      bool
	modelAnswered    bool
	answers          int
	eventReads       int
	readsAfterAnswer int
	modelToolResult  string
	answerReadback   chan struct{}
}

func NewQuestionCommands(pending, answered, arguments, receipt json.RawMessage, failRefresh bool) *QuestionCommands {
	return &QuestionCommands{
		pending: pending, answered: answered, arguments: arguments, receipt: receipt,
		failRefresh: failRefresh, answerReadback: make(chan struct{}),
	}
}

func (f *QuestionCommands) Tools() []llm.Tool {
	return []llm.Tool{{Name: "answer_question", Schema: json.RawMessage(`{"type":"object"}`)}}
}

func (f *QuestionCommands) Instructions() string { return "Use saved question answers." }

func (f *QuestionCommands) Complete(_ context.Context, _ string, history []llm.Message, tools []llm.Tool, _ func(string)) (llm.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.modelAnswered {
		if last := history[len(history)-1]; last.Role == "tool" && last.Name == "answer_question" {
			f.modelToolResult = last.Text
		}
		return llm.Message{Role: "assistant", Text: "Used the saved answer."}, nil
	}
	for _, tool := range tools {
		if tool.Name == "answer_question" {
			f.modelAnswered = true
			return llm.Message{Role: "assistant", Calls: []llm.Call{{ID: "answer-1", Name: tool.Name, Arguments: f.arguments}}}, nil
		}
	}
	return llm.Message{}, errors.New("question fallback did not expose the answer tool")
}

func (f *QuestionCommands) Call(ctx context.Context, name string, _ json.RawMessage) (bridge.Result, error) {
	f.mu.Lock()
	switch name {
	case "answer_question":
		f.answers++
		f.mu.Unlock()
		return bridge.Result{Data: f.receipt, Text: string(f.receipt)}, nil
	case "operation_events":
		f.eventReads++
		first := f.eventReads == 1
		f.mu.Unlock()
		if !first {
			select {
			case <-f.answerReadback:
			case <-ctx.Done():
				return bridge.Result{}, ctx.Err()
			}
		}
		return bridge.Result{Data: json.RawMessage(`{"next_cursor":"fixture"}`)}, nil
	default:
		f.mu.Unlock()
		return bridge.Result{}, errors.New("unexpected question fixture tool")
	}
}

func (f *QuestionCommands) Operation(ctx context.Context, _ string) (bridge.Operation, error) {
	if err := ctx.Err(); err != nil {
		return bridge.Operation{}, err
	}
	f.mu.Lock()
	data := f.pending
	failed := false
	if f.answers != 0 {
		f.readsAfterAnswer++
		data = f.answered
		if f.readsAfterAnswer == 1 {
			failed = f.failRefresh
			close(f.answerReadback)
		}
	}
	f.mu.Unlock()
	if failed {
		return bridge.Operation{}, errors.New("fixture readback unavailable")
	}
	var operation bridge.Operation
	err := json.Unmarshal(data, &operation)
	operation.Raw = append(json.RawMessage(nil), data...)
	return operation, err
}

func (f *QuestionCommands) Snapshot() (answers, readsAfterAnswer int, modelToolResult string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answers, f.readsAfterAnswer, f.modelToolResult
}
