package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func (c *Client) responses(ctx context.Context, system string, history []Message, tools []Tool, delta func(string)) (Message, error) {
	input := make([]any, 0, len(history))
	for _, message := range history {
		switch {
		case len(message.Native) != 0:
			// Replay every output item in order, including encrypted reasoning,
			// message phases and function calls. No server-side conversation is needed.
			var items []json.RawMessage
			if err := json.Unmarshal(message.Native, &items); err != nil {
				return Message{}, fmt.Errorf("invalid Responses history: %w", err)
			}
			for _, item := range items {
				input = append(input, item)
			}
		case message.Role == "tool":
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message.CallID, "output": message.Text})
		default:
			input = append(input, map[string]any{"role": message.Role, "content": message.Text})
		}
	}
	body := map[string]any{
		"model": c.config.Model, "instructions": system, "input": input,
		"stream": true, "store": false, "include": []string{"reasoning.encrypted_content"},
	}
	if len(tools) != 0 {
		functions := make([]any, 0, len(tools))
		for _, tool := range tools {
			// MCP schemas may contain optional properties; do not normalize them
			// into required fields through Responses' default strict mode.
			functions = append(functions, map[string]any{
				"type": "function", "name": tool.Name, "description": tool.Description,
				"parameters": tool.Schema, "strict": false,
			})
		}
		body["tools"] = functions
	}
	response, err := c.request(ctx, http.MethodPost, "/responses", body)
	if err != nil {
		return Message{}, err
	}
	defer response.Body.Close()
	var output json.RawMessage
	err = stream(response.Body, func(data []byte) error {
		if bytes.Equal(data, []byte("[DONE]")) {
			return nil
		}
		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				Status            string          `json:"status"`
				Output            json.RawMessage `json:"output"`
				Error             json.RawMessage `json:"error"`
				IncompleteDetails json.RawMessage `json:"incomplete_details"`
			} `json:"response"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return fmt.Errorf("invalid model stream: %w", err)
		}
		switch event.Type {
		case "response.output_text.delta", "response.refusal.delta":
			if delta != nil {
				delta(event.Delta)
			}
		case "response.completed":
			if event.Response.Status != "completed" {
				return errors.New("model response did not complete; no tool calls were executed")
			}
			output = event.Response.Output
		case "response.failed":
			return c.diagnosticError("model response failed; no tool calls were executed", event.Response.Error)
		case "response.incomplete":
			return c.diagnosticError("model response incomplete; no tool calls were executed", event.Response.IncompleteDetails)
		case "error":
			return c.diagnosticError("model reported a streaming error", data)
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	if len(output) == 0 || bytes.Equal(output, []byte("null")) {
		return Message{}, errors.New("model stream ended before completion; no tool calls were executed")
	}
	return responseMessage(output)
}

// Only the completed response is authoritative. Partial streamed calls are
// never returned to the chat loop for execution.
func responseMessage(output json.RawMessage) (Message, error) {
	var items []struct {
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	}
	if err := json.Unmarshal(output, &items); err != nil {
		return Message{}, fmt.Errorf("invalid model output: %w", err)
	}
	result := Message{Role: "assistant", Native: output}
	var text strings.Builder
	for _, item := range items {
		switch item.Type {
		case "function_call":
			result.Calls = append(result.Calls, Call{ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(item.Arguments)})
		case "message":
			for _, content := range item.Content {
				switch content.Type {
				case "output_text":
					text.WriteString(content.Text)
				case "refusal":
					text.WriteString(content.Refusal)
				}
			}
		}
	}
	if err := validateCalls(result.Calls); err != nil {
		return Message{}, err
	}
	result.Text = text.String()
	return result, nil
}
