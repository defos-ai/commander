package chat

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/id"
)

func (e *Engine) call(ctx context.Context, call llm.Call) bridge.Result {
	if call.ID == "" {
		call.ID = id.New()
	}
	// Capture a stable ID before a potentially ambiguous network failure.
	if call.Name == "start_operation" {
		call.Arguments = withOperationID(call.Arguments)
	}
	e.emit(Event{Kind: "tool", Text: call.Name, CallID: call.ID})
	if call.Name == "answer_question" {
		if err := e.checkQuestionAnswer(ctx, call.Arguments); err != nil {
			result := bridge.Result{IsError: true, Text: err.Error()}
			e.reportCall(call, result)
			return result
		}
	}
	result, err := e.commander.Call(ctx, call.Name, call.Arguments)
	if err != nil {
		result = bridge.Result{IsError: true, Text: "Tool failed: " + err.Error() + ". Execution may have been accepted; inspect state before retrying."}
		if call.Name == "start_operation" {
			if id := operationID(call.Arguments); id != "" {
				result.Text += " Operation ID: " + id
			}
		}
	}
	e.reportCall(call, result)
	if !result.IsError {
		e.follow(call, result)
	} else if call.Name == "start_operation" {
		if id := operationID(call.Arguments); id != "" {
			e.emit(Event{Kind: "notice", Text: "Check operation " + id + " before starting it again."})
		}
	}
	return result
}

func (e *Engine) checkQuestionAnswer(ctx context.Context, arguments json.RawMessage) error {
	var request struct {
		OperationID string `json:"operation_id"`
		QuestionID  string `json:"question_id"`
		Answer      string `json:"answer"`
	}
	if json.Unmarshal(arguments, &request) != nil || !id.Valid(request.OperationID) || !id.Valid(request.QuestionID) || strings.TrimSpace(request.Answer) == "" {
		return errors.New("Answer requires an operation, question and nonblank text.")
	}
	operation, err := e.commander.Operation(ctx, request.OperationID)
	if err != nil || operation.ID != request.OperationID {
		return errors.New("Could not refresh the question; no answer submitted. Inspect get_operation before trying again.")
	}
	select {
	case e.updates <- operation:
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, question := range fallbackQuestions(operation, time.Now()) {
		if question.ID == request.QuestionID {
			return nil
		}
	}
	return errors.New("Question is no longer pending or model answering is not yet allowed. Inspect get_operation and use any saved answer; no answer was submitted.")
}

func (e *Engine) reportCall(call llm.Call, result bridge.Result) {
	kind, label := "success", call.Name+" completed"
	if result.IsError {
		kind, label = "error", call.Name+" failed"
		if detail := strings.TrimSpace(result.Text); detail != "" {
			label += ": " + detail
		}
	}
	e.emit(Event{Kind: kind, Text: label, CallID: call.ID})
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
	if call.Name == "answer_question" {
		var operation bridge.Operation
		if json.Unmarshal(result.Data, &operation) == nil && operation.ID == id {
			operation.Raw = result.Data
			select {
			case e.updates <- operation:
			case <-e.ctx.Done():
			}
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
