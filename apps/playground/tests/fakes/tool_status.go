package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolStatusReply struct {
	Result bridge.Result
	Error  error
}

type toolStatusGate struct {
	done chan struct{}
	once sync.Once
}

type ToolStatusCommander struct {
	name    string
	replies map[string]ToolStatusReply
	gates   map[string]*toolStatusGate
}

func NewToolStatusCommander(name string, replies map[string]ToolStatusReply) *ToolStatusCommander {
	commander := &ToolStatusCommander{name: name, replies: make(map[string]ToolStatusReply), gates: make(map[string]*toolStatusGate)}
	for key, reply := range replies {
		reply.Result.Data = bytes.Clone(reply.Result.Data)
		commander.replies[key] = reply
		commander.gates[key] = &toolStatusGate{done: make(chan struct{})}
	}
	return commander
}

func (c *ToolStatusCommander) Tools() []llm.Tool {
	tools := []llm.Tool{{Name: c.name, Schema: json.RawMessage(`{"type":"object"}`)}}
	if c.name != "list_operations" {
		tools = append(tools, llm.Tool{Name: "list_operations", Schema: json.RawMessage(`{"type":"object"}`)})
	}
	return tools
}

func (*ToolStatusCommander) Instructions() string { return "" }

func (*ToolStatusCommander) Operation(context.Context, string) (bridge.Operation, error) {
	return bridge.Operation{}, fmt.Errorf("tool status scenario does not watch operations")
}

func (c *ToolStatusCommander) Call(ctx context.Context, name string, arguments json.RawMessage) (bridge.Result, error) {
	var input struct{ Key string }
	if err := json.Unmarshal(arguments, &input); err != nil {
		return bridge.Result{}, err
	}
	reply, exists := c.replies[input.Key]
	if !exists || name != c.name && name != "list_operations" {
		return bridge.Result{}, fmt.Errorf("unexpected tool status call: %s", name)
	}
	select {
	case <-c.gates[input.Key].done:
		reply.Result.Data = bytes.Clone(reply.Result.Data)
		return reply.Result, reply.Error
	case <-ctx.Done():
		return bridge.Result{}, ctx.Err()
	}
}

func (c *ToolStatusCommander) Release(key string) {
	gate := c.gates[key]
	gate.once.Do(func() { close(gate.done) })
}

func (c *ToolStatusCommander) ReleaseAll() {
	for key := range c.gates {
		c.Release(key)
	}
}

func (c *ToolStatusCommander) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "tool-status-fixture", Version: "1"}, nil)
	for _, tool := range c.Tools() {
		server.AddTool(&mcp.Tool{Name: tool.Name, InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := c.Call(ctx, request.Params.Name, request.Params.Arguments)
			if err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{IsError: result.IsError, Content: []mcp.Content{&mcp.TextContent{Text: result.Text}}}, nil
		})
	}
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

type ToolStatusModel struct {
	calls         []llm.Call
	before, after string
	round         atomic.Int32
}

func NewToolStatusModel(calls []llm.Call, before, after string) *ToolStatusModel {
	model := &ToolStatusModel{before: before, after: after}
	for _, call := range calls {
		call.Arguments = bytes.Clone(call.Arguments)
		model.calls = append(model.calls, call)
	}
	return model
}

func (m *ToolStatusModel) next() llm.Message {
	if m.round.Add(1) == 1 {
		calls := make([]llm.Call, len(m.calls))
		for i, call := range m.calls {
			call.Arguments = bytes.Clone(call.Arguments)
			calls[i] = call
		}
		return llm.Message{Role: "assistant", Text: m.before, Calls: calls}
	}
	return llm.Message{Role: "assistant", Text: m.after}
}

func (m *ToolStatusModel) Complete(_ context.Context, _ string, _ []llm.Message, _ []llm.Tool, delta func(string)) (llm.Message, error) {
	message := m.next()
	delta(message.Text)
	return message, nil
}

func (m *ToolStatusModel) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		message := m.next()
		delta := map[string]any{"content": message.Text}
		finish := "stop"
		if len(message.Calls) > 0 {
			finish = "tool_calls"
			var calls []any
			for i, call := range message.Calls {
				calls = append(calls, map[string]any{"index": i, "id": call.ID, "type": "function", "function": map[string]any{
					"name": call.Name, "arguments": string(call.Arguments),
				}})
			}
			delta["tool_calls"] = calls
		}
		WriteModelDelta(w, delta, finish)
	})
}
