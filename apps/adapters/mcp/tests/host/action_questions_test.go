package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	fixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/operations"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPModelAnswersRunningActionQuestionAfterOperatorWindow(t *testing.T) {
	fixture := fixtures.NewActionQuestion(t)
	call := func(name string, input, output any) {
		t.Helper()
		fixtures.MissionCall(t, fixture.Context, fixture.Session, name, input, output)
	}
	var op operations.Operation
	call("start_operation", map[string]any{"mission_name": "action-question", "input": map[string]any{}}, &op)
	var task operations.Task
	call("assign_task", map[string]any{"operation_id": op.ID, "action_name": "confirm-action", "input": map[string]any{}}, &task)
	fixtures.AwaitMCPLateArtifact(t, "question from the running action", func() bool {
		call("get_operation", map[string]any{"operation_id": op.ID}, &op)
		return len(op.Questions) == 1
	})
	question := op.Questions[0].Clone()
	if question.TaskID != task.ID || question.CommandID != task.CommandID || question.Key != "confirm-action" || !question.Pending() ||
		len(op.Tasks) != 1 || op.Tasks[0].Status != operations.TaskRunning || op.Tasks[0].Continuation != nil {
		t.Fatalf("action question changed its running execution or requested a continuation: %+v", op)
	}
	answer := map[string]any{"operation_id": op.ID, "question_id": question.ID, "answer": "Yes"}

	result, err := fixture.Session.CallTool(fixture.Context, &sdkmcp.CallToolParams{Name: "answer_question", Arguments: answer})

	text := questionToolText(t, result, err)
	if !result.IsError || !strings.Contains(text, "question_operator_waiting") {
		t.Fatalf("model bypassed the running question's operator window: %s", text)
	}
	call("get_operation", map[string]any{"operation_id": op.ID}, &op)
	if len(op.Questions) != 1 || !op.Questions[0].Pending() || op.Questions[0].ID != question.ID || op.Tasks[0].Status != operations.TaskRunning {
		t.Fatal("rejected early answer changed the question or stopped its action")
	}
	wait := time.NewTimer(time.Until(question.FallbackAt))
	defer wait.Stop()
	select {
	case <-wait.C:
	case <-fixture.Context.Done():
		t.Fatal("question window did not finish within fixture lifetime")
	}

	call("answer_question", answer, &op)
	var finished operations.Task
	fixtures.AwaitMCPLateArtifact(t, "same action resumed after its answer", func() bool {
		call("get_task", map[string]any{"operation_id": op.ID, "task_id": task.ID}, &finished)
		return finished.Status == operations.TaskSucceeded
	})

	if finished.ID != task.ID || finished.CommandID != task.CommandID || finished.AgentID != fixture.AgentID || finished.Result == nil || fixture.Work.Invocations() != 1 {
		t.Fatalf("question answer lost the running execution or executed it again: %+v", finished)
	}
	var output map[string]string
	if err := json.Unmarshal(finished.Result.Output, &output); err != nil || output["question_id"] != question.ID || output["repeated_question_id"] != question.ID ||
		output["answer"] != "Yes" || output["answered_by"] != "model" {
		t.Fatalf("action did not receive its saved model answer or reused key created another question: %+v, %v", output, err)
	}
	call("get_operation", map[string]any{"operation_id": op.ID}, &op)
	if len(op.Tasks) != 1 || len(op.Questions) != 1 || op.Questions[0].ID != question.ID || op.Questions[0].Status != operations.QuestionAnswered ||
		op.Questions[0].AnsweredBy != operations.AnswerModel || op.Questions[0].CommandID != task.CommandID || op.Questions[0].Key != question.Key {
		t.Fatalf("saved question or execution identity changed: %+v", op)
	}
	saved, err := fixture.Executions.Get(fixture.Context, fixture.AgentID, task.CommandID)
	if err != nil || saved.Result == nil {
		t.Fatalf("resumed execution result was not persisted: %v", err)
	}
	call("cancel_operation", map[string]any{"operation_id": op.ID}, &op)
}
