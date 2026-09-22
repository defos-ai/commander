package mcp_test

import (
	"testing"
	"time"

	"commander/framework/domain/operations"
)

func TestQuestionsLatestAnswerAndAssignmentBlocking(t *testing.T) {
	// Arrange: two operator questions are pending on one operation.
	a := connect(t)
	op := a.start("https://example.test")
	var first, latest operations.Question
	a.call("ask_question", object{"operation_id": op.ID, "text": "Which release matters?"}, &first)
	a.call("ask_question", object{"operation_id": op.ID, "text": "What is the review priority?", "options": []string{"Accessibility", "SEO", "Links", "Appearance"}}, &latest)

	// Act: read the active question through both query interfaces and try assigning work.
	saved := a.get(op.ID)
	var list struct {
		Operations []operationDetail `json:"operations"`
	}
	a.call("list_operations", object{}, &list)
	a.fails("assign_task", object{"operation_id": op.ID, "action_name": "check-seo", "input": object{"url": "https://example.test"}}, "pending question")

	// Assert: only the latest is presented, with the number of other pending questions.
	if saved.Status != operations.Waiting || saved.LastQuestion == nil || saved.LastQuestion.ID != latest.ID || saved.RemainingQuestions != 1 || len(saved.Tasks) != 0 {
		t.Fatalf("pending question view: %+v", saved)
	}
	for _, question := range []operations.Question{first, latest} {
		if question.Status != operations.QuestionPending || question.ExpiresAt.Sub(question.AskedAt) != 2*time.Minute {
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

	// Act: record real operator text, then answer the remaining question.
	answer := "Prioritize keyboard accessibility.\nKeep the review read-only."
	a.call("answer_question", object{"operation_id": op.ID, "question_id": latest.ID, "answer": answer}, nil)
	a.fails("answer_question", object{"operation_id": op.ID, "question_id": latest.ID, "answer": "Replace the answer"}, "already answered")
	saved = a.get(op.ID)

	// Assert: answering reveals the earlier question and preserves exact guidance.
	if saved.LastQuestion == nil || saved.LastQuestion.ID != first.ID || saved.RemainingQuestions != 0 || len(saved.Questions) != 2 {
		t.Fatalf("remaining question view: %+v", saved)
	}
	answered := saved.Questions[1]
	if answered.Status != operations.QuestionAnswered || answered.Answer != answer || answered.AnsweredAt == nil {
		t.Fatalf("operator answer changed: %+v", answered)
	}
	a.call("answer_question", object{"operation_id": op.ID, "question_id": first.ID, "answer": "The current release"}, nil)
	saved = a.get(op.ID)
	if saved.LastQuestion != nil || saved.RemainingQuestions != 0 || saved.Status != operations.Planning {
		t.Fatalf("answered questions still block work: %+v", saved)
	}
	// An unavailable test action stays queued; no external website is contacted.
	a.task(op.ID, "unavailable-test-action", "https://example.test")
	a.call("cancel_operation", object{"operation_id": op.ID}, nil)
}

func TestLinkedQuestionReturnsDecisionToModel(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "operator answers"
		if expired {
			name = "operator does not answer before timeout"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange: a completed check has a model decision requiring operator guidance.
			a := connect(t)
			url := website(t)
			op := a.start(url)
			a.task(op.ID, "check-seo", url)
			ready := a.wait(op.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
			task := modelDecision(ready)
			if task.Status != operations.TaskSucceeded {
				t.Fatalf("fixture check failed: %+v", task)
			}
			originalVersion := task.Continuation.Version
			args := object{"operation_id": op.ID, "task_id": task.ID, "version": originalVersion, "text": "Is this intentionally a release preview?", "options": []string{"Yes", "No"}}
			if expired {
				args["timeout_seconds"] = 3
			}
			var question operations.Question
			a.call("ask_question", args, &question)
			waiting := a.get(op.ID)
			continuation := waiting.Tasks[0].Continuation
			if continuation == nil || continuation.DecisionBy != operations.DecisionUser || continuation.Status != operations.ContinuationWaiting || continuation.QuestionID != question.ID {
				t.Fatalf("question did not pause decision: %+v", waiting)
			}
			a.fails("resolve_continuation", object{"operation_id": op.ID, "task_id": task.ID, "version": originalVersion, "actions": []object{}}, "pending")

			// Act: either answer through MCP or allow the controller's deadline to expire.
			wantStatus := operations.QuestionAnswered
			answer := "Yes, the indexing restriction is intentional."
			if expired {
				wantStatus, answer = operations.QuestionExpired, ""
				if question.ExpiresAt.Sub(question.AskedAt) != 3*time.Second {
					t.Fatalf("custom deadline ignored: %+v", question)
				}
			} else {
				a.call("answer_question", object{"operation_id": op.ID, "question_id": question.ID, "answer": answer}, nil)
			}
			resumed := a.wait(op.ID, func(op operationDetail) bool {
				return len(op.Questions) == 1 && op.Questions[0].Status == wantStatus && modelDecision(op) != nil
			})

			// Assert: history is retained, no work is authorized, and stale decisions stay invalid.
			recorded := resumed.Questions[0]
			decision := modelDecision(resumed)
			if resumed.LastQuestion != nil || resumed.RemainingQuestions != 0 || len(resumed.Tasks) != 1 || decision.Continuation.Version != originalVersion+2 || recorded.Answer != answer || (recorded.AnsweredAt == nil) != expired {
				t.Fatalf("incorrect decision handoff: %+v", resumed)
			}
			a.fails("answer_question", object{"operation_id": op.ID, "question_id": question.ID, "answer": "Late replacement"}, map[bool]string{false: "already answered", true: "expired"}[expired])
			a.fails("resolve_continuation", object{"operation_id": op.ID, "task_id": task.ID, "version": originalVersion + 1, "actions": []object{}}, "pending")
			a.resolve(op.ID, *decision, []object{})
			a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": "Reviewed the SEO evidence and operator question", "findings": []object{}}}, nil)
		})
	}
}
