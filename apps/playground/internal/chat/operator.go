package chat

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
)

func (e *Engine) control(name string, args map[string]any) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.emit(Event{Kind: "control_start"})
		defer e.emit(Event{Kind: "control_end"})
		operationID, _ := args["operation_id"].(string)
		questionID, _ := args["question_id"].(string)
		answerEnd := Event{Kind: "answer_end", OperationID: operationID, QuestionID: questionID}
		if name == "answer_question" {
			e.emit(Event{Kind: "answer_start", OperationID: operationID, QuestionID: questionID})
		}
		encoded, _ := json.Marshal(args)
		result := e.call(e.ctx, llm.Call{Name: name, Arguments: encoded})
		if result.IsError {
			if name == "answer_question" {
				answerEnd.Text = result.Text
				e.emit(answerEnd)
			}
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
		} else if name == "answer_question" {
			answerEnd.Answer, _ = args["answer"].(string)
			e.emit(answerEnd)
			if operation, err := e.commander.Operation(e.ctx, operationID); err == nil {
				select {
				case e.updates <- operation:
				case <-e.ctx.Done():
					return
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
			if question.ID != id || !question.Pending(time.Now()) {
				continue
			}
			if number, err := strconv.Atoi(answer); err == nil && number > 0 && number <= len(question.Options) {
				answer = question.Options[number-1]
			}
			e.submitAnswer(answerRequest{operation.ID, id, answer}, operations)
			return
		}
	}
	e.emit(Event{Kind: "error", Text: "No pending question with that ID. Use /watch <operation-id> first."})
}

func (e *Engine) submitAnswer(answer answerRequest, operations map[string]bridge.Operation) {
	operation, exists := operations[answer.operationID]
	if exists && !operation.Terminal() {
		questions := operation.Questions
		if operation.LastQuestion != nil {
			questions = append(append([]bridge.Question(nil), questions...), *operation.LastQuestion)
		}
		for _, question := range questions {
			if question.ID == answer.questionID && question.Pending(time.Now()) {
				e.control("answer_question", map[string]any{"operation_id": answer.operationID, "question_id": answer.questionID, "answer": answer.answer})
				return
			}
		}
	}
	const message = "This question is no longer pending or has not been loaded."
	e.emit(Event{Kind: "answer_end", OperationID: answer.operationID, QuestionID: answer.questionID, Text: message})
	e.emit(Event{Kind: "error", Text: message})
}

// runAction submits explicit operator input without asking the model to choose
// or approve an action. Controller validation and readiness checks still apply.
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
