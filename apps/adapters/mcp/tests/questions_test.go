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
	a.Call("ask_question", fixtures.Object{"operation_id": op.ID, "text": "What is the review priority?", "options": []string{"Counts", "Summary", "Notes", "Other"}}, &latest)

	saved := a.Get(op.ID)
	var list struct {
		Operations []fixtures.OperationDetail `json:"operations"`
	}
	a.Call("list_operations", fixtures.Object{}, &list)
	a.Fails("assign_task", fixtures.Object{"operation_id": op.ID, "action_name": "write-summary", "input": fixtures.SummaryInput()}, "pending question")

	if saved.Status != operations.Waiting || saved.LastQuestion == nil || saved.LastQuestion.ID != latest.ID || saved.RemainingQuestions != 1 || len(saved.Tasks) != 0 {
		t.Fatalf("pending question view: %+v", saved)
	}
	for _, question := range []operations.Question{first, latest} {
		if question.Status != operations.QuestionPending || question.AnsweredBy != "" || question.ExpiresAt.Sub(question.AskedAt) != 2*time.Minute {
			t.Fatalf("question default lifecycle: %+v", question)
		}
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

	answer := "Prioritize keyboard accessibility.\nKeep the review read-only."
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
	a.Call("answer_question", fixtures.Object{"operation_id": op.ID, "question_id": first.ID, "answer": "The current release"}, nil)
	saved = a.Get(op.ID)
	if saved.LastQuestion != nil || saved.RemainingQuestions != 0 || saved.Status != operations.Planning {
		t.Fatalf("answered questions still block work: %+v", saved)
	}
	a.Task(op.ID, "unavailable-test-action", fixtures.Object{})
	a.Call("cancel_operation", fixtures.Object{"operation_id": op.ID}, nil)
}

func TestLinkedQuestionReturnsDecisionToModel(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "model submits answer"
		if expired {
			name = "no answer before timeout"
		}
		t.Run(name, func(t *testing.T) {
			a := fixtures.Connect(t)
			op := a.Start("Linked question fixture")
			a.Task(op.ID, "write-summary", fixtures.SummaryInput())
			ready := a.Wait(op.ID, func(op fixtures.OperationDetail) bool { return fixtures.ModelDecision(op) != nil })
			task := fixtures.ModelDecision(ready)
			if task.Status != operations.TaskSucceeded {
				t.Fatalf("fixture check failed: %+v", task)
			}
			originalVersion := task.Continuation.Version
			args := fixtures.Object{"operation_id": op.ID, "task_id": task.ID, "version": originalVersion, "text": "Should the summary be accepted?", "options": []string{"Yes", "No"}}
			if expired {
				args["timeout_seconds"] = 3
			}
			var question operations.Question
			a.Call("ask_question", args, &question)
			waiting := a.Get(op.ID)
			continuation := waiting.Tasks[0].Continuation
			if continuation == nil || continuation.DecisionBy != operations.DecisionUser || continuation.Status != operations.ContinuationWaiting || continuation.QuestionID != question.ID {
				t.Fatalf("question did not pause decision: %+v", waiting)
			}
			a.Fails("resolve_continuation", fixtures.Object{"operation_id": op.ID, "task_id": task.ID, "version": originalVersion, "actions": []fixtures.Object{}}, "pending")

			wantStatus := operations.QuestionAnswered
			answer := "Yes, accept the summary."
			if expired {
				wantStatus, answer = operations.QuestionExpired, ""
				if question.ExpiresAt.Sub(question.AskedAt) != 3*time.Second {
					t.Fatalf("custom deadline ignored: %+v", question)
				}
			} else {
				a.Call("answer_question", fixtures.Object{"operation_id": op.ID, "question_id": question.ID, "answer": answer}, nil)
			}
			resumed := a.Wait(op.ID, func(op fixtures.OperationDetail) bool {
				return len(op.Questions) == 1 && op.Questions[0].Status == wantStatus && fixtures.ModelDecision(op) != nil
			})

			recorded := resumed.Questions[0]
			decision := fixtures.ModelDecision(resumed)
			if resumed.LastQuestion != nil || resumed.RemainingQuestions != 0 || len(resumed.Tasks) != 1 || decision.Continuation.Version != originalVersion+2 || recorded.Answer != answer || (recorded.AnsweredAt == nil) != expired {
				t.Fatalf("incorrect decision handoff: %+v", resumed)
			}
			wantOrigin := operations.AnswerModel
			if expired {
				wantOrigin = ""
			}
			if recorded.AnsweredBy != wantOrigin {
				t.Fatalf("question answer origin: got %q, want %q", recorded.AnsweredBy, wantOrigin)
			}
			a.Fails("answer_question", fixtures.Object{"operation_id": op.ID, "question_id": question.ID, "answer": "Late replacement"}, map[bool]string{false: "already answered", true: "expired"}[expired])
			a.Fails("resolve_continuation", fixtures.Object{"operation_id": op.ID, "task_id": task.ID, "version": originalVersion + 1, "actions": []fixtures.Object{}}, "pending")
			a.Resolve(op.ID, *decision, []fixtures.Object{})
			a.Call("complete_operation", fixtures.Object{"operation_id": op.ID, "output": fixtures.Object{"summary": "Reviewed the saved evidence and operator question"}}, nil)
		})
	}
}
