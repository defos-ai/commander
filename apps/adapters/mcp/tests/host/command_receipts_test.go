package host_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
)

func TestCompactCommandReceiptsKeepSavedEvidenceAndOperatorApproval(t *testing.T) {
	a := fixtures.Host(t)
	input := json.RawMessage(`{"evidence":9007199254740993}`)
	output := json.RawMessage(`{"summary":"review\u0020done","finding":9007199254740993}`)
	var receipt fixtures.CommandReceipt
	a.Call("start_operation", fixtures.Object{"mission_name": "review", "input": input}, &receipt)
	operationID := receipt.ID
	if operationID == "" || receipt.Status != operations.Planning || receipt.Preflight == nil || !receipt.Preflight.Ready || receipt.Progress.TotalTasks != 0 {
		t.Fatalf("start receipt lost identity, readiness or progress: %+v", receipt)
	}
	started := a.Get(operationID)
	if started.Mission.Name != "review" || !bytes.Equal(started.Input, input) || len(started.Events) == 0 {
		t.Fatalf("get_operation lost the saved start evidence: %+v", started)
	}
	task := a.Task(operationID, "inspect", fixtures.Object{})
	ready := a.Wait(operationID, func(op fixtures.OperationDetail) bool { return fixtures.ModelDecision(op) != nil })
	version := fixtures.ModelDecision(ready).Continuation.Version
	var question operations.Question
	a.Call("ask_question", fixtures.Object{"operation_id": operationID, "task_id": task.ID, "version": version, "text": "Accept this evidence?", "timeout_seconds": 1}, &question)
	afterWindow := a.Wait(operationID, func(op fixtures.OperationDetail) bool { return !time.Now().Before(question.FallbackAt) })
	if afterWindow.Questions[0].Status != operations.QuestionPending || afterWindow.Tasks[0].Continuation.Version != version+1 {
		t.Fatalf("fallback changed saved question or decision without an answer: %+v", afterWindow)
	}

	receipt = fixtures.CommandReceipt{}
	a.Call("answer_question", fixtures.Object{"operation_id": operationID, "question_id": question.ID, "answer": "Accept"}, &receipt)
	answered := a.Get(operationID)
	decision := fixtures.ModelDecision(answered)
	if receipt.ID != operationID || receipt.Progress.PendingQuestions != 0 || receipt.Progress.PendingDecisions != 1 || decision == nil || decision.Continuation.Version != version+2 || answered.Questions[0].AnsweredBy != operations.AnswerModel || answered.Questions[0].Answer != "Accept" {
		t.Fatalf("answer receipt or saved current decision is incorrect: receipt=%+v operation=%+v", receipt, answered)
	}
	a.Fails("resolve_continuation", fixtures.Object{"operation_id": operationID, "task_id": task.ID, "version": version, "actions": []fixtures.Object{}}, "pending")
	receipt = fixtures.CommandReceipt{}
	a.Call("resolve_continuation", fixtures.Object{"operation_id": operationID, "task_id": task.ID, "version": decision.Continuation.Version, "actions": []fixtures.Object{}}, &receipt)
	var savedTask operations.Task
	a.Call("get_task", fixtures.Object{"operation_id": operationID, "task_id": task.ID}, &savedTask)
	if receipt.Progress.PendingDecisions != 0 || savedTask.Result == nil || string(savedTask.Result.Output) != `{"ok":true}` || savedTask.Continuation.Status != operations.ContinuationResolved {
		t.Fatalf("detail read lost the accepted execution: %+v", savedTask)
	}
	receipt = fixtures.CommandReceipt{}
	a.Call("complete_operation", fixtures.Object{"operation_id": operationID, "output": output}, &receipt)
	var report reports.Report
	a.Call("get_report", fixtures.Object{"operation_id": operationID}, &report)
	if receipt.Status != operations.Completed || !receipt.Progress.Settled || receipt.Progress.FinishedTasks != 1 || receipt.ReportID != report.ID || !bytes.Equal(report.Output, output) {
		t.Fatalf("completion receipt or saved report changed: receipt=%+v report=%+v", receipt, report)
	}

	operator := fixtures.HostMission()
	operator.Name = "operator-review"
	operator.Policies.Completion = &policies.CompletionPolicy{DecisionBy: policies.Operator}
	a.Call("create_mission", fixtures.MissionDefinition(operator), nil)
	receipt = fixtures.CommandReceipt{}
	a.Call("start_operation", fixtures.Object{"mission_name": operator.Name, "input": fixtures.Object{}}, &receipt)
	operatorID := receipt.ID
	receipt = fixtures.CommandReceipt{}
	a.Call("complete_operation", fixtures.Object{"operation_id": operatorID, "output": output}, &receipt)
	pending := a.Get(operatorID)
	if receipt.Status.Terminal() || receipt.ReportID != "" || !strings.Contains(receipt.Message, "Awaiting operator approval") || !bytes.Equal(pending.CompletionDraft, output) || len(pending.Output) != 0 || pending.CompletedBy != "" {
		t.Fatalf("operator draft was completed or lost on detail read: receipt=%+v operation=%+v", receipt, pending)
	}
	a.Fails("get_report", fixtures.Object{"operation_id": operatorID}, "report")
	receipt = fixtures.CommandReceipt{}
	a.Call("cancel_operation", fixtures.Object{"operation_id": operatorID}, &receipt)
	if receipt.Status != operations.Cancelled || !receipt.Progress.Settled {
		t.Fatalf("cancel receipt did not settle operator review: %+v", receipt)
	}

	receipt = fixtures.CommandReceipt{}
	a.Call("start_operation", fixtures.Object{"mission_name": "review", "input": fixtures.Object{}}, &receipt)
	failedID := receipt.ID
	receipt = fixtures.CommandReceipt{}
	a.Call("fail_operation", fixtures.Object{"operation_id": failedID, "reason": "Evidence is unavailable"}, &receipt)
	if receipt.ID != failedID || receipt.Status != operations.Failed || receipt.Error != "Evidence is unavailable" || !receipt.Progress.Settled || a.Get(failedID).Error != receipt.Error {
		t.Fatalf("failure receipt lost the saved reason or settlement: %+v", receipt)
	}
}
