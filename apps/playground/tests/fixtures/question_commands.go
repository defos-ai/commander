package fixtures

import (
	"encoding/json"
	"testing"
	"time"

	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/framework/id"
)

func NewQuestionCommands(t *testing.T, failRefresh bool) (*fakes.QuestionCommands, string) {
	t.Helper()
	operationID, questionID := id.New(), id.New()
	now := time.Now().UTC()
	operation := bridge.Operation{
		ID: operationID, Status: "waiting", UpdatedAt: now,
		Tasks:     []bridge.Task{{ID: id.New(), AgentID: "fixture-agent-00000001", ActionName: "inspect", Status: "running"}},
		Questions: []bridge.Question{{ID: questionID, Status: bridge.QuestionPending, Text: "Which report format?", FallbackAt: now.Add(-time.Minute)}},
		Progress:  bridge.Progress{TotalTasks: 1, PendingQuestions: 1},
	}
	pending := questionOperationData(t, operation)
	operation.Status = "running"
	operation.UpdatedAt = now.Add(time.Second)
	operation.Questions[0].Status = bridge.QuestionAnswered
	operation.Questions[0].Answer = "Summary"
	operation.Questions[0].AnsweredBy = "model"
	operation.Progress.PendingQuestions = 0
	answered := questionOperationData(t, operation)
	arguments, err := json.Marshal(map[string]string{"operation_id": operationID, "question_id": questionID, "answer": "Summary"})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := json.Marshal(map[string]any{"id": operationID, "status": operation.Status, "progress": operation.Progress})
	if err != nil {
		t.Fatal(err)
	}
	return fakes.NewQuestionCommands(pending, answered, arguments, receipt, failRefresh), operationID
}

func questionOperationData(t *testing.T, operation bridge.Operation) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot["mission"] = json.RawMessage(`{"name":"question-fixture"}`)
	data, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
