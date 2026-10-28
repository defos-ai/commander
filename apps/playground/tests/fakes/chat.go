package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
)

type ReviewModel struct {
	started chan struct{}
	block   bool
}

func NewReviewModel(block bool) *ReviewModel {
	return &ReviewModel{started: make(chan struct{}, 4), block: block}
}

func (m *ReviewModel) Started() <-chan struct{} { return m.started }

func (m *ReviewModel) Complete(ctx context.Context, _ string, _ []llm.Message, _ []llm.Tool, _ func(string)) (llm.Message, error) {
	select {
	case m.started <- struct{}{}:
	case <-ctx.Done():
		return llm.Message{}, ctx.Err()
	}
	if m.block {
		<-ctx.Done()
		return llm.Message{}, ctx.Err()
	}
	return llm.Message{Role: "assistant", Text: "Operation reviewed"}, nil
}

type Commander struct {
	operation atomic.Pointer[bridge.Operation]
	updates   chan struct{}
}

func NewCommander(operation bridge.Operation) *Commander {
	commander := &Commander{updates: make(chan struct{}, 4)}
	commander.Publish(operation)
	return commander
}

func (c *Commander) Publish(operation bridge.Operation) {
	operation.Raw, _ = json.Marshal(operation)
	var snapshot bridge.Operation
	if err := json.Unmarshal(operation.Raw, &snapshot); err != nil {
		panic(err)
	}
	snapshot.Raw = operation.Raw
	c.operation.Store(&snapshot)
	c.updates <- struct{}{}
}

func (*Commander) Tools() []llm.Tool    { return nil }
func (*Commander) Instructions() string { return "" }

func (c *Commander) Operation(context.Context, string) (bridge.Operation, error) {
	current := c.operation.Load()
	var snapshot bridge.Operation
	if err := json.Unmarshal(current.Raw, &snapshot); err != nil {
		return bridge.Operation{}, err
	}
	snapshot.Raw = bytes.Clone(current.Raw)
	return snapshot, nil
}

func (c *Commander) Call(ctx context.Context, name string, _ json.RawMessage) (bridge.Result, error) {
	if name != "operation_events" {
		return bridge.Result{}, fmt.Errorf("unexpected operation command: %s", name)
	}
	select {
	case <-c.updates:
		return bridge.Result{Data: json.RawMessage(`{"next_cursor":"next"}`)}, nil
	case <-ctx.Done():
		return bridge.Result{}, ctx.Err()
	}
}
