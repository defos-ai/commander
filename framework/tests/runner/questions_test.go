package runner_test

import (
	"testing"
	"time"

	"commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/actions"
	"commander/framework/domain/operations"
)

func TestRecoveryExpiresOverdueQuestionWithoutInventingAnswer(t *testing.T) {
	config := fixture()
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	task := assign(t, r, op.ID, "inspect")
	receive(t, calls).reply <- output("inspect")
	settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	question, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, TaskID: task.ID,
		Version: settled.Tasks[0].Continuation.Version, Text: "How should we continue?"})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != operations.Waiting || waiting.Tasks[0].Continuation.DecisionBy != operations.DecisionUser {
		t.Fatalf("question did not suspend decision: %+v", waiting)
	}

	closeRunner(t, r)
	if _, err := config.OperationStore.(*operationStore).Update(t.Context(), waiting.ID, func(op *operations.Operation) error {
		op.Questions[0].ExpiresAt = time.Now().Add(-time.Second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	r = newRunner(t, config, exec)
	expired, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, late := r.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: op.ID, QuestionID: question.ID, Answer: "Continue"})

	q, c := expired.Questions[0], expired.Tasks[0].Continuation
	if late == nil || q.Status != operations.QuestionExpired || q.Answer != "" || q.AnsweredAt != nil || len(expired.Tasks) != 1 || expired.Status != operations.Planning {
		t.Fatalf("expiry invented an answer or left work waiting: %+v, late=%v", expired, late)
	}
	if c.Status != operations.ContinuationPending || c.DecisionBy != operations.DecisionModel || c.Version <= waiting.Tasks[0].Continuation.Version {
		t.Fatalf("decision not returned: %+v", c)
	}
	_, err = r.Resolve(t.Context(), commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: task.ID, Version: c.Version, Actions: []actions.NextAction{}})
	if err != nil {
		t.Fatalf("model cannot resolve expired question: %v", err)
	}
	count := 0
	for _, event := range expired.Events {
		if event.Type == operations.QuestionExpiredEvent {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate expiration events: %d", count)
	}
}
