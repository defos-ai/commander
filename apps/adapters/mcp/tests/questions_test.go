package mcp_test

import (
	"bytes"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
)

func TestQuestionsLatestAnswerAndAssignmentBlocking(t *testing.T) {
	a := connect(t)
	op := a.start("https://example.test")
	var first, latest operations.Question
	a.call("ask_question", object{"operation_id": op.ID, "text": "Which release matters?"}, &first)
	a.call("ask_question", object{"operation_id": op.ID, "text": "What is the review priority?", "options": []string{"Accessibility", "SEO", "Links", "Appearance"}}, &latest)

	saved := a.get(op.ID)
	var list struct {
		Operations []operationDetail `json:"operations"`
	}
	a.call("list_operations", object{}, &list)
	a.fails("assign_task", object{"operation_id": op.ID, "action_name": "check-seo", "input": object{"url": "https://example.test"}}, "pending question")

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

	answer := "Prioritize keyboard accessibility.\nKeep the review read-only."
	a.call("answer_question", object{"operation_id": op.ID, "question_id": latest.ID, "answer": answer}, nil)
	a.fails("answer_question", object{"operation_id": op.ID, "question_id": latest.ID, "answer": "Replace the answer"}, "already answered")
	saved = a.get(op.ID)

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

func TestDiscoveryRedirectWaitsForOperatorBeforeLimitedReport(t *testing.T) {
	a := connect(t)
	var outsideRequests atomic.Int32
	url := serveWebsite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/outside" {
			outsideRequests.Add(1)
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><title>Outside requested scope</title></html>"))
			return
		}
		// Use the fixture's reachable IP as a different hostname from the
		// Compose host alias. Local test stacks may use an IP already.
		host, port, err := net.SplitHostPort(r.Context().Value(http.LocalAddrContextKey).(net.Addr).String())
		if err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture address", http.StatusInternalServerError)
			return
		}
		requestedHost, _, _ := net.SplitHostPort(r.Host)
		if strings.EqualFold(host, requestedHost) {
			host = "host.docker.internal"
			if address := net.ParseIP(requestedHost); address != nil && address.IsLoopback() {
				host = "localhost"
			}
		}
		http.Redirect(w, r, "http://"+net.JoinHostPort(host, port)+"/outside", http.StatusFound)
	}))
	op := a.start(url)
	if len(op.Mission.Automations["discover-pages"].Run) == 0 {
		t.Fatal("fixture mission has no discovery automation")
	}

	a.task(op.ID, "discover-pages", url)
	failed := a.wait(op.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	decision := modelDecision(failed)
	if failed.Status != operations.Planning || len(failed.Tasks) != 1 || decision.Status != operations.TaskFailed || !strings.Contains(decision.Error, "leaves website scope") || outsideRequests.Load() != 0 {
		t.Fatalf("redirect did not preserve a scoped model decision: operation=%+v, outside requests=%d", failed, outsideRequests.Load())
	}
	originalVersion := decision.Continuation.Version
	var question operations.Question
	a.call("ask_question", object{"operation_id": op.ID, "task_id": decision.ID, "version": originalVersion,
		"text": "The website redirects to another hostname. Should the review include it?", "options": []string{"Review the destination", "Keep the original scope"}}, &question)
	waiting := a.get(op.ID)
	continuation := waiting.Tasks[0].Continuation
	if waiting.Status != operations.Waiting || continuation.DecisionBy != operations.DecisionUser || continuation.Status != operations.ContinuationWaiting || continuation.QuestionID != question.ID {
		t.Fatalf("redirect question did not wait for the operator: %+v", waiting)
	}

	const answer = "Keep the original scope"
	a.call("answer_question", object{"operation_id": op.ID, "question_id": question.ID, "answer": answer}, nil)
	resumed := a.get(op.ID)
	decision = modelDecision(resumed)
	if resumed.Status != operations.Planning || decision == nil || decision.Continuation.Version != originalVersion+2 || len(resumed.Questions) != 1 || resumed.Questions[0].Status != operations.QuestionAnswered || resumed.Questions[0].Answer != answer {
		t.Fatalf("operator choice did not resume the current decision: %+v", resumed)
	}
	a.fails("resolve_continuation", object{"operation_id": op.ID, "task_id": decision.ID, "version": originalVersion, "actions": []object{}}, "pending")
	a.resolve(op.ID, *decision, []object{})
	const limitation = "Discovery was blocked by a cross-host redirect; the operator declined to expand scope, so no pages were reviewed."
	var completed operations.Operation
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": "Review ended with the operator's requested scope limitation", "findings": []object{}, "limitations": []string{limitation}}}, &completed)
	a.fails("ask_question", object{"operation_id": op.ID, "text": "Should the report be shown?"}, "operation has finished")
	var report reports.Report
	a.call("get_report", object{"operation_id": op.ID}, &report)
	if completed.Status != operations.Completed || len(completed.Tasks) != 1 || report.ID != completed.ReportID || report.OperationID != op.ID || !bytes.Contains(report.Output, []byte(limitation)) || outsideRequests.Load() != 0 {
		t.Fatalf("limited report or redirect scope was lost: operation=%+v, report=%+v, outside requests=%d", completed, report, outsideRequests.Load())
	}
}
