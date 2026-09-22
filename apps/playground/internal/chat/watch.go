package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	bridge "commander/apps/playground/internal/mcp"
)

func (e *Engine) watch(id string) {
	var cursor uint64
	var version uint64
	first, failed := true, false
	for e.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(e.ctx, 40*time.Second)
		var err error
		if !first {
			args, _ := json.Marshal(map[string]any{"operation_id": id, "after_sequence": cursor, "wait_seconds": 25})
			var result bridge.Result
			result, err = e.commander.Call(ctx, "operation_events", args)
			if err == nil && result.IsError {
				err = fmt.Errorf("%s", result.Text)
			}
			if err == nil {
				var page struct {
					Next uint64 `json:"next_sequence"`
				}
				err = json.Unmarshal(result.Data, &page)
				if err == nil {
					cursor = page.Next
				}
			}
		}
		if err == nil {
			var operation bridge.Operation
			operation, err = e.commander.Operation(ctx, id)
			if err == nil {
				if first {
					cursor = uint64(len(operation.Events))
				}
				if first || operation.Version != version || failed {
					select {
					case e.updates <- operation:
					case <-e.ctx.Done():
						cancel()
						return
					}
				}
				version = operation.Version
				first = false
				if failed {
					e.emit(Event{Kind: "success", Text: "Operation updates resumed: " + id})
				}
				failed = false
				if operation.Settled() {
					cancel()
					return
				}
			}
		}
		cancel()
		if err != nil {
			if !failed {
				e.emit(Event{Kind: "error", Text: "Operation updates unavailable for " + id + "; retrying in the background."})
			}
			failed = true
		}
		// Also avoids a busy loop against servers that return an empty page early.
		delay := 250 * time.Millisecond
		if failed {
			delay = 3 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-e.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (e *Engine) control(name string, args map[string]any) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.emit(Event{Kind: "control_start"})
		defer e.emit(Event{Kind: "control_end"})
		e.emit(Event{Kind: "tool", Text: name})
		encoded, _ := json.Marshal(args)
		result, err := e.commander.Call(e.ctx, name, encoded)
		if err != nil {
			e.emit(Event{Kind: "error", Text: err.Error()})
			return
		}
		if result.IsError {
			e.emit(Event{Kind: "error", Text: result.Text})
			return
		}
		if name == "list_operations" {
			var list struct {
				Operations []struct {
					ID, Status string
					Mission    struct{ Name string }
				}
			}
			if err := json.Unmarshal(result.Data, &list); err != nil {
				e.emit(Event{Kind: "error", Text: "Could not read operations"})
				return
			}
			text := "No operations yet. Ask Commander to start a mission."
			if len(list.Operations) > 0 {
				var lines []string
				for _, operation := range list.Operations {
					lines = append(lines, fmt.Sprintf("%s  %s  %s", operation.ID, operation.Status, operation.Mission.Name))
				}
				text = strings.Join(lines, "\n")
			}
			e.emit(Event{Kind: "notice", Text: text})
		} else {
			e.emit(Event{Kind: "success", Text: name + " completed"})
			if name == "assign_task" {
				id, _ := args["operation_id"].(string)
				select {
				case e.watchRequests <- id:
				case <-e.ctx.Done():
				}
			}
			if name == "answer_question" {
				text := fmt.Sprintf("The operator answered question %s in operation %s: %s. Inspect the operation and continue using their answer.", args["question_id"], args["operation_id"], args["answer"])
				select {
				case e.controls <- text:
				case <-e.ctx.Done():
				}
			}
		}
	}()
}

func (e *Engine) answer(text string, operations map[string]bridge.Operation) {
	id, answer, ok := strings.Cut(strings.TrimSpace(text), " ")
	answer = strings.TrimSpace(answer)
	if !ok || answer == "" {
		e.emit(Event{Kind: "error", Text: "Use /answer <question-id> <answer or option number>"})
		return
	}
	for _, operation := range operations {
		for _, question := range operation.Questions {
			if question.ID != id || question.AnsweredAt != nil {
				continue
			}
			if number, err := strconv.Atoi(answer); err == nil && number > 0 && number <= len(question.Options) {
				answer = question.Options[number-1]
			}
			e.control("answer_question", map[string]any{"operation_id": operation.ID, "question_id": id, "answer": answer})
			return
		}
	}
	e.emit(Event{Kind: "error", Text: "No pending question with that ID. Use /watch <operation-id> first."})
}

// runAction submits explicit operator input without asking the model to choose
// or approve an action. Controller validation and permission checks still apply.
func (e *Engine) runAction(text string) {
	operationID, rest, ok := strings.Cut(strings.TrimSpace(text), " ")
	actionName, raw, hasInput := strings.Cut(strings.TrimSpace(rest), " ")
	var input map[string]json.RawMessage
	if !ok || !hasInput || operationID == "" || actionName == "" || json.Unmarshal([]byte(strings.TrimSpace(raw)), &input) != nil || input == nil {
		e.emit(Event{Kind: "error", Text: "Use /run <operation-id> <action-name> <json-object>"})
		return
	}
	e.control("assign_task", map[string]any{"operation_id": operationID, "action_name": actionName, "input": input})
}
