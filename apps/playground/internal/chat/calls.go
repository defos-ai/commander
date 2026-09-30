package chat

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"commander/apps/playground/internal/llm"
	bridge "commander/apps/playground/internal/mcp"
)

func (e *Engine) call(ctx context.Context, call llm.Call) bridge.Result {
	// Capture a stable ID before a potentially ambiguous network failure.
	if call.Name == "start_operation" {
		call.Arguments = withOperationID(call.Arguments)
	}
	e.emit(Event{Kind: "tool", Text: call.Name})
	result, err := e.commander.Call(ctx, call.Name, call.Arguments)
	if err != nil {
		result = bridge.Result{IsError: true, Text: "Tool failed: " + err.Error() + ". Execution may have been accepted; inspect state before retrying."}
		if call.Name == "start_operation" {
			if id := operationID(call.Arguments); id != "" {
				result.Text += " Operation ID: " + id
			}
		}
	}
	e.reportCall(call.Name, result)
	if !result.IsError {
		e.follow(call, result)
	} else if call.Name == "start_operation" {
		if id := operationID(call.Arguments); id != "" {
			e.emit(Event{Kind: "notice", Text: "Check operation " + id + " before starting it again."})
		}
	}
	return result
}

func (e *Engine) reportCall(name string, result bridge.Result) {
	kind, label := "success", name+" completed"
	if result.IsError {
		kind, label = "error", name+" failed"
		if detail := strings.TrimSpace(result.Text); detail != "" {
			label += ": " + detail
		}
	}
	e.emit(Event{Kind: kind, Text: label})
}

func operationID(arguments json.RawMessage) string {
	var input struct {
		ID string `json:"operation_id"`
	}
	_ = json.Unmarshal(arguments, &input)
	return input.ID
}

func (e *Engine) follow(call llm.Call, result bridge.Result) {
	if call.Name == "get_report" {
		return
	}
	id := operationID(call.Arguments)
	if call.Name == "start_operation" {
		var operation struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(result.Data, &operation)
		if operation.ID != "" {
			id = operation.ID
		}
	}
	if id != "" {
		select {
		case e.watchRequests <- id:
		case <-e.ctx.Done():
		}
	}
}

func withOperationID(raw json.RawMessage) json.RawMessage {
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil || input == nil {
		return raw
	}
	if value, ok := input["operation_id"].(string); ok && value != "" {
		return raw
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return raw
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	input["operation_id"] = fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	encoded, err := json.Marshal(input)
	if err != nil {
		return raw
	}
	return encoded
}
