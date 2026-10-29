package runner_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestRecoveryRetainsQuestionAfterOperatorWindow(t *testing.T) {
	config := fixtures.Config()
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	task := fixtures.Assign(t, r, op.ID, "inspect")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	question, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, TaskID: task.ID,
		Version: settled.Tasks[0].Continuation.Version, Text: "How should we continue?"})
	if err != nil {
		t.Fatal(err)
	}
	fixtures.CloseRunner(t, r)
	waiting, err := config.OperationStore.(*fakes.OperationStore).Update(t.Context(), op.ID, func(op *operations.Operation) error {
		op.Questions[0].AskedAt = time.Now().Add(-2 * time.Minute)
		op.Questions[0].FallbackAt = time.Now().Add(-time.Minute)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	r = fixtures.NewRunner(t, config, exec)
	recovered, err := r.Get(t.Context(), op.ID)

	if err != nil || recovered.Status != operations.Waiting || !reflect.DeepEqual(recovered.Questions, waiting.Questions) || !reflect.DeepEqual(recovered.Tasks, waiting.Tasks) {
		t.Fatalf("recovery changed the unanswered question or its blocked continuation: %+v, %v", recovered, err)
	}
	progress := recovered.AssessProgress(time.Now())
	if progress.PendingQuestions != 1 || progress.PendingDecisions != 0 || progress.LastQuestion == nil || progress.LastQuestion.ID != question.ID {
		t.Fatalf("elapsed operator window hid the pending question or released a decision: %+v", progress)
	}
	_, err = r.Resolve(t.Context(), commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: task.ID,
		Version: recovered.Tasks[0].Continuation.Version, Actions: []actions.NextAction{}})
	if !errors.Is(err, operations.ErrContinuationConflict) {
		t.Fatalf("unanswered question allowed the model to bypass its waiting continuation: %v", err)
	}

	answered, err := r.Answer(t.Context(), commands.AnswerQuestionCommand{AnsweredBy: operations.AnswerModel,
		OperationID: op.ID, QuestionID: question.ID, Answer: "Continue"})

	if err != nil || answered.Questions[0].AnsweredBy != operations.AnswerModel || answered.Questions[0].Status != operations.QuestionAnswered || answered.Tasks[0].Continuation.DecisionBy != operations.DecisionModel || answered.Tasks[0].Continuation.Status != operations.ContinuationPending || answered.Tasks[0].Continuation.Version != waiting.Tasks[0].Continuation.Version+1 {
		t.Fatalf("model answer after recovery did not resume the linked decision: %+v, %v", answered, err)
	}
	_, err = r.Resolve(t.Context(), commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: task.ID,
		Version: answered.Tasks[0].Continuation.Version, Actions: []actions.NextAction{}})
	if err != nil {
		t.Fatalf("answered question did not release its decision: %v", err)
	}
	select {
	case call := <-calls:
		t.Fatalf("question recovery invented follow-up work: %+v", call)
	default:
	}
}

func TestModelAnswerWaitsForOperatorWindow(t *testing.T) {
	config := fixtures.Config()
	exec, _ := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	question, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, Text: "Continue?"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := r.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, answerErr := r.Answer(t.Context(), commands.AnswerQuestionCommand{AnsweredBy: operations.AnswerModel,
		OperationID: op.ID, QuestionID: question.ID, Answer: "Continue"})
	retained, err := r.Get(t.Context(), op.ID)

	if !errors.Is(answerErr, operations.ErrQuestionOperatorWaiting) || err != nil || !reflect.DeepEqual(retained, before) {
		t.Fatalf("early model answer changed the pending question: %+v, answer=%v, get=%v", retained, answerErr, err)
	}

	answered, err := r.Answer(t.Context(), commands.AnswerQuestionCommand{AnsweredBy: operations.AnswerOperator,
		OperationID: op.ID, QuestionID: question.ID, Answer: "Proceed"})

	if err != nil || answered.Questions[0].Status != operations.QuestionAnswered || answered.Questions[0].AnsweredBy != operations.AnswerOperator || answered.Questions[0].AnsweredAt == nil || !answered.Questions[0].AnsweredAt.Before(question.FallbackAt) {
		t.Fatalf("operator could not answer immediately during its window: %+v, %v", answered, err)
	}
}
