package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
)

func (c *Client) anthropic(ctx context.Context, system string, history []Message, tools []Tool, delta func(string)) (Message, error) {
	var messages []any
	// Consecutive tool results must share one user message in the Messages API.
	for i := 0; i < len(history); i++ {
		message := history[i]
		if len(message.Native) != 0 {
			messages = append(messages, message.Native)
			continue
		}
		if message.Role == "tool" {
			var results []any
			for ; i < len(history) && history[i].Role == "tool"; i++ {
				item := history[i]
				results = append(results, map[string]any{"type": "tool_result", "tool_use_id": item.CallID, "content": item.Text})
			}
			i--
			messages = append(messages, map[string]any{"role": "user", "content": results})
		} else {
			messages = append(messages, map[string]any{"role": message.Role, "content": message.Text})
		}
	}
	functions := make([]any, 0, len(tools))
	for _, tool := range tools {
		functions = append(functions, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.Schema})
	}
	body := map[string]any{"model": c.config.Model, "system": system, "messages": messages, "stream": true, "max_tokens": 4096}
	if len(functions) != 0 {
		body["tools"] = functions
	}
	response, err := c.request(ctx, http.MethodPost, "/messages", body)
	if err != nil {
		return Message{}, err
	}
	defer response.Body.Close()
	blocks := map[int]map[string]any{}
	arguments := map[int]string{}
	finished, stopReason := false, ""
	err = stream(response.Body, func(data []byte) error {
		var event struct {
			Error json.RawMessage `json:"error"`
			Type  string          `json:"type"`
			Index int             `json:"index"`
			Block map[string]any  `json:"content_block"`
			Delta struct {
				Type, Text, Thinking, Signature string
				PartialJSON                     string `json:"partial_json"`
				StopReason                      string `json:"stop_reason"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return fmt.Errorf("invalid model stream: %w", err)
		}
		switch event.Type {
		case "error":
			return c.diagnosticError("model reported a streaming error", event.Error)
		case "content_block_start":
			if event.Block == nil {
				return errors.New("missing content block")
			}
			blocks[event.Index] = event.Block
		case "content_block_delta":
			block := blocks[event.Index]
			if block == nil {
				return errors.New("missing content block start")
			}
			switch event.Delta.Type {
			case "text_delta":
				text, _ := block["text"].(string)
				block["text"] = text + event.Delta.Text
				if delta != nil {
					delta(event.Delta.Text)
				}
			case "input_json_delta":
				arguments[event.Index] += event.Delta.PartialJSON
			case "thinking_delta":
				text, _ := block["thinking"].(string)
				block["thinking"] = text + event.Delta.Thinking
			case "signature_delta":
				text, _ := block["signature"].(string)
				block["signature"] = text + event.Delta.Signature
			}
		case "message_delta":
			stopReason = event.Delta.StopReason
		case "message_stop":
			finished = true
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	if !finished {
		return Message{}, errors.New("model stream ended before completion; no tool calls were executed")
	}
	if stopReason == "max_tokens" {
		return Message{}, errors.New("model response reached its output limit")
	}
	var indexes []int
	for index := range blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var content []any
	result := Message{Role: "assistant"}
	for _, index := range indexes {
		block := blocks[index]
		if block["type"] == "tool_use" {
			if raw := arguments[index]; raw != "" {
				var input any
				if err := json.Unmarshal([]byte(raw), &input); err != nil {
					return Message{}, errors.New("model returned invalid tool arguments")
				}
				block["input"] = input
			}
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			input, err := json.Marshal(block["input"])
			if err != nil {
				return Message{}, err
			}
			result.Calls = append(result.Calls, Call{ID: id, Name: name, Arguments: input})
		}
		if text, ok := block["text"].(string); ok {
			result.Text += text
		}
		content = append(content, block)
	}
	if err := validateCalls(result.Calls); err != nil {
		return Message{}, err
	}
	result.Native, err = json.Marshal(map[string]any{"role": "assistant", "content": content})
	return result, err
}
