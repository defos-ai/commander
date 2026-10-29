package mcp_test

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestQuestionsLatestAnswerAndAssignmentBlocking(t *testing.T) {
	a := fixtures.Connect(t)
	op := a.Start("Question barrier fixture")
	var first, latest operations.Question
	a.Call("ask_question", fixtures.Object{"operation_id": op.ID, "text": "Which release matters?"}, &first)
	a.Call("ask_question", fixtures.Object{"operation_id": op.ID, "text": "What is the review priority?", "timeout_seconds": 3, "options": []string{"Counts", "Summary", "Notes", "Other"}}, &latest)

	answer := "Prioritize keyboard accessibility.\nKeep the review read-only."
	a.Fails("answer_question", fixtures.Object{"operation_id": op.ID, "question_id": latest.ID, "answer": answer}, "operator")

	saved := a.Get(op.ID)
	var list struct {
		Operations []fixtures.OperationDetail `json:"operations"`
	}
	a.Call("list_operations", fixtures.Object{}, &list)
	a.Fails("assign_task", fixtures.Object{"operation_id": op.ID, "action_name": "write-summary", "input": fixtures.SummaryInput()}, "pending question")

	if saved.Status != operations.Waiting || saved.LastQuestion == nil || saved.LastQuestion.ID != latest.ID || saved.RemainingQuestions != 1 || len(saved.Tasks) != 0 {
		t.Fatalf("pending question view: %+v", saved)
	}
	if first.Status != operations.QuestionPending || first.AnsweredBy != "" || first.FallbackAt.Sub(first.AskedAt) != 2*time.Minute || latest.FallbackAt.Sub(latest.AskedAt) != 3*time.Second {
		t.Fatalf("question waiting windows: first=%+v latest=%+v", first, latest)
	}
	found := false
	for _, summary := range list.Operations {
		if summary.ID == op.ID {
			found = true
			if summary.LastQuestion == nil || summary.LastQuestion.ID != latest.ID || summary.RemainingQuestions != 1 {
				t.Fatalf("list_operations question view: %+v", summary)
			}
		}
	}
	if !found {
		t.Fatal("operation missing from list_operations")
	}

	a.Wait(op.ID, func(op fixtures.OperationDetail) bool { return !time.Now().Before(latest.FallbackAt) })
	a.Call("answer_question", fixtures.Object{"operation_id": op.ID, "question_id": latest.ID, "answer": answer}, nil)
	a.Fails("answer_question", fixtures.Object{"operation_id": op.ID, "question_id": latest.ID, "answer": "Replace the answer"}, "already answered")
	saved = a.Get(op.ID)

	if saved.LastQuestion == nil || saved.LastQuestion.ID != first.ID || saved.RemainingQuestions != 0 || len(saved.Questions) != 2 {
		t.Fatalf("remaining question view: %+v", saved)
	}
	answered := saved.Questions[1]
	if answered.Status != operations.QuestionAnswered || answered.Answer != answer || answered.AnsweredAt == nil || answered.AnsweredBy != operations.AnswerModel {
		t.Fatalf("model answer changed: %+v", answered)
	}
	operatorAnswer := "The current release."
	if _, err := fixtures.AnswerAsOperator(a.Context, op.ID, first.ID, operatorAnswer); err != nil {
		t.Fatal(err)
	}
	saved = a.Get(op.ID)
	operator := saved.Questions[0]
	if operator.Status != operations.QuestionAnswered || operator.Answer != operatorAnswer || operator.AnsweredBy != operations.AnswerOperator ||
		operator.AnsweredAt == nil || !operator.AnsweredAt.Before(first.FallbackAt) {
		t.Fatalf("operator answer did not settle the default waiting window: %+v", operator)
	}
	a.Call("cancel_operation", fixtures.Object{"operation_id": op.ID}, nil)
}

func TestLinkedQuestionWaitsForModelAnswerAfterOperatorWindow(t *testing.T) {
	a := fixtures.Connect(t)
	op := a.Start("Linked question fixture")
	a.Task(op.ID, "write-summary", fixtures.SummaryInput())
	ready := a.Wait(op.ID, func(op fixtures.OperationDetail) bool { return fixtures.ModelDecision(op) != nil })
	task := fixtures.ModelDecision(ready)
	if task.Status != operations.TaskSucceeded {
		t.Fatalf("fixture check failed: %+v", task)
	}
	originalVersion := task.Continuation.Version
	var question operations.Question
	a.Call("ask_question", fixtures.Object{"operation_id": op.ID, "task_id": task.ID, "version": originalVersion, "text": "Should the summary be accepted?", "options": []string{"Yes", "No"}, "timeout_seconds": 3}, &question)
	waiting := a.Get(op.ID)
	continuation := waiting.Tasks[0].Continuation
	if continuation == nil || continuation.DecisionBy != operations.DecisionUser || continuation.Status != operations.ContinuationWaiting || continuation.QuestionID != question.ID {
		t.Fatalf("question did not pause decision: %+v", waiting)
	}
	answer := "Yes, accept the summary."
	a.Fails("answer_question", fixtures.Object{"operation_id": op.ID, "question_id": question.ID, "answer": answer}, "operator")
	a.Fails("resolve_continuation", fixtures.Object{"operation_id": op.ID, "task_id": task.ID, "version": originalVersion, "actions": []fixtures.Object{}}, "pending")

	afterWindow := a.Wait(op.ID, func(op fixtures.OperationDetail) bool { return !time.Now().Before(question.FallbackAt) })
	if question.FallbackAt.Sub(question.AskedAt) != 3*time.Second || afterWindow.Questions[0].Status != operations.QuestionPending || afterWindow.Questions[0].Answer != "" || afterWindow.Tasks[0].Continuation.Status != operations.ContinuationWaiting || afterWindow.Tasks[0].Continuation.Version != originalVersion+1 {
		t.Fatalf("waiting window released the continuation or invented an answer: %+v", afterWindow)
	}
	a.Call("answer_question", fixtures.Object{"operation_id": op.ID, "question_id": question.ID, "answer": answer}, nil)
	resumed := a.Wait(op.ID, func(op fixtures.OperationDetail) bool { return fixtures.ModelDecision(op) != nil })

	recorded := resumed.Questions[0]
	decision := fixtures.ModelDecision(resumed)
	if resumed.LastQuestion != nil || resumed.RemainingQuestions != 0 || len(resumed.Tasks) != 1 || decision.Continuation.Version != originalVersion+2 || recorded.Status != operations.QuestionAnswered || recorded.Answer != answer || recorded.AnsweredAt == nil || recorded.AnsweredBy != operations.AnswerModel {
		t.Fatalf("incorrect model-answer handoff: %+v", resumed)
	}
	a.Fails("answer_question", fixtures.Object{"operation_id": op.ID, "question_id": question.ID, "answer": "Late replacement"}, "already answered")
	a.Fails("resolve_continuation", fixtures.Object{"operation_id": op.ID, "task_id": task.ID, "version": originalVersion + 1, "actions": []fixtures.Object{}}, "pending")
	a.Resolve(op.ID, *decision, []fixtures.Object{})
	a.Call("complete_operation", fixtures.Object{"operation_id": op.ID, "output": fixtures.Object{"summary": "Reviewed the saved evidence and question"}}, nil)
}
