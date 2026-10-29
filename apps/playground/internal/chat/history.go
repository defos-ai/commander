package chat

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
)

func PortableHistory(messages []llm.Message) []llm.Message {
	result := restoreHistory(messages)
	for i := range result {
		message := &result[i]
		message.Native = nil
		if len(message.Calls) != 0 {
			calls, _ := json.Marshal(message.Calls)
			message.Text = strings.TrimSpace(message.Text + "\n\nHistorical tool requests (already issued; inspect current state before considering a retry):\n" + string(calls))
			message.Calls = nil
		}
		if message.Role == "tool" {
			message.Role = "user"
			message.Text = "Historical tool result for " + message.Name + " (" + message.CallID + "):\n" + message.Text
			message.CallID, message.Name = "", ""
		}
	}
	return result
}

func copyHistory(messages []llm.Message) []llm.Message {
	result := make([]llm.Message, len(messages))
	for i, message := range messages {
		result[i] = message
		result[i].Native = bytes.Clone(message.Native)
		if bytes.Equal(bytes.TrimSpace(result[i].Native), []byte("null")) {
			result[i].Native = nil
		}
		result[i].Calls = append([]llm.Call(nil), message.Calls...)
		for j := range result[i].Calls {
			result[i].Calls[j].Arguments = bytes.Clone(message.Calls[j].Arguments)
		}
	}
	return result
}

// A saved tool request may have reached Commander before Playground stopped.
// Complete its protocol envelope without inventing an execution result or retry.
func restoreHistory(messages []llm.Message) []llm.Message {
	var result []llm.Message
	var pending []llm.Call
	finish := func() {
		for _, call := range pending {
			result = append(result, llm.Message{Role: "tool", CallID: call.ID, Name: call.Name,
				Text: "Playground stopped before this tool result was saved. Its execution outcome is unknown; inspect current Commander state before considering a retry."})
		}
		pending = nil
	}
	for _, message := range copyHistory(messages) {
		if message.Role != "tool" {
			finish()
		}
		if message.Role == "tool" {
			matched := false
			for i, call := range pending {
				if call.ID == message.CallID {
					pending = append(pending[:i], pending[i+1:]...)
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		result = append(result, message)
		if message.Role == "assistant" {
			pending = append([]llm.Call(nil), message.Calls...)
		}
	}
	finish()
	return result
}

func (e *Engine) checkpoint(history []llm.Message) {
	e.emit(Event{Kind: "history", History: copyHistory(history)})
}
