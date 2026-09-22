package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
)

func (c *Client) compatible(ctx context.Context, system string, history []Message, tools []Tool, delta func(string)) (Message, error) {
	messages := []any{map[string]any{"role": "system", "content": system}}
	for _, message := range history {
		if len(message.Native) != 0 {
			messages = append(messages, message.Native)
			continue
		}
		item := map[string]any{"role": message.Role, "content": message.Text}
		if message.Role == "tool" {
			item["tool_call_id"] = message.CallID
		}
		messages = append(messages, item)
	}
	functions := make([]any, 0, len(tools))
	for _, tool := range tools {
		functions = append(functions, map[string]any{"type": "function", "function": tool})
	}
	body := map[string]any{"model": c.config.Model, "messages": messages, "stream": true}
	if len(functions) != 0 {
		body["tools"] = functions
	}
	response, err := c.request(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return Message{}, err
	}
	defer response.Body.Close()
	message := map[string]any{"role": "assistant"}
	calls := map[int]map[string]any{}
	finished := false
	finishReason := ""
	err = stream(response.Body, func(data []byte) error {
		if bytes.Equal(data, []byte("[DONE]")) {
			return nil
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta        map[string]any `json:"delta"`
				FinishReason *string        `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("invalid model stream: %w", err)
		}
		if len(chunk.Error) != 0 && string(chunk.Error) != "null" {
			return c.diagnosticError("model reported a streaming error", chunk.Error)
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != nil {
			finished, finishReason = true, *choice.FinishReason
		}
		if text, ok := choice.Delta["content"].(string); ok && delta != nil {
			delta(text)
		}
		if chunks, ok := choice.Delta["tool_calls"].([]any); ok {
			for _, value := range chunks {
				part, ok := value.(map[string]any)
				if !ok {
					return errors.New("invalid tool call chunk")
				}
				position, ok := part["index"].(float64)
				if !ok || position < 0 || position > 128 {
					return errors.New("invalid tool call index")
				}
				index := int(position)
				if calls[index] == nil {
					calls[index] = map[string]any{}
				}
				delete(part, "index")
				mergeDelta(calls[index], part)
			}
		}
		delete(choice.Delta, "tool_calls")
		mergeDelta(message, choice.Delta)
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	if !finished {
		return Message{}, errors.New("model stream ended before completion; no tool calls were executed")
	}
	if finishReason == "length" || finishReason == "content_filter" {
		return Message{}, fmt.Errorf("model response stopped: %s", finishReason)
	}
	var indexes []int
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var wireCalls []any
	result := Message{Role: "assistant"}
	result.Text, _ = message["content"].(string)
	for _, index := range indexes {
		item := calls[index]
		wireCalls = append(wireCalls, item)
		var wire struct {
			ID       string                           `json:"id"`
			Function struct{ Name, Arguments string } `json:"function"`
		}
		encoded, _ := json.Marshal(item)
		if err := json.Unmarshal(encoded, &wire); err != nil {
			return Message{}, err
		}
		result.Calls = append(result.Calls, Call{ID: wire.ID, Name: wire.Function.Name, Arguments: json.RawMessage(wire.Function.Arguments)})
	}
	if len(wireCalls) != 0 {
		message["tool_calls"] = wireCalls
	}
	if err := validateCalls(result.Calls); err != nil {
		return Message{}, err
	}
	result.Native, err = json.Marshal(message)
	return result, err
}

func mergeDelta(target, part map[string]any) {
	for key, value := range part {
		if value == nil {
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			current, _ := target[key].(map[string]any)
			if current == nil {
				current = map[string]any{}
				target[key] = current
			}
			mergeDelta(current, nested)
		} else if text, ok := value.(string); ok && (key == "content" || key == "arguments" || key == "reasoning_content" || key == "refusal") {
			previous, _ := target[key].(string)
			target[key] = previous + text
		} else {
			target[key] = value
		}
	}
}
