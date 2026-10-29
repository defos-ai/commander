package runner_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestQuestionWriteFailurePreservesLinkedDecision(t *testing.T) {
	for _, transition := range []string{"ask", "answer"} {
		t.Run(transition, func(t *testing.T) {
			config := fixtures.Config()
			exec, calls := fakes.ControlledExecutor()
			r := fixtures.NewRunner(t, config, exec)
			before := operationAwaitingDecision(t, r, calls)
			ask := commands.AskQuestionCommand{OperationID: before.ID, TaskID: before.Tasks[0].ID,
				Version: before.Tasks[0].Continuation.Version, Text: "Continue?", Options: []string{"yes", "no"}}
			answer := commands.AnswerQuestionCommand{AnsweredBy: operations.AnswerOperator, OperationID: before.ID, Answer: "yes"}
			if transition == "answer" {
				question, err := r.Ask(t.Context(), ask)
				if err != nil {
					t.Fatal(err)
				}
				answer.QuestionID = question.ID
				before, err = r.Get(t.Context(), before.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			change := func() error {
				if transition == "ask" {
					_, err := r.Ask(t.Context(), ask)
					return err
				}
				_, err := r.Answer(t.Context(), answer)
				return err
			}
			failure := errors.New("database unavailable")
			config.OperationStore.(*fakes.OperationStore).FailNextUpdate(failure)

			changeErr := change()
			stored, err := r.Get(t.Context(), before.ID)
			if !errors.Is(changeErr, failure) || err != nil || !reflect.DeepEqual(stored, before) {
				t.Fatalf("failed %s changed question or linked continuation: operation=%+v, change=%v, get=%v", transition, stored, changeErr, err)
			}
			events, err := r.Events(t.Context(), before.ID, 0, false)
			if err != nil || !reflect.DeepEqual(events, before.Events) {
				t.Fatalf("failed %s changed runner events: %+v, %v", transition, events, err)
			}

			if err := change(); err != nil {
				t.Fatalf("%s was not retryable: %v", transition, err)
			}
			retried, err := r.Get(t.Context(), before.ID)
			if err != nil || len(retried.Questions) != 1 || retried.Tasks[0].Continuation.Version != before.Tasks[0].Continuation.Version+1 || len(retried.Events) != len(before.Events)+1 {
				t.Fatalf("retry lost or duplicated the question transition: %+v, %v", retried, err)
			}
			question, continuation := retried.Questions[0], retried.Tasks[0].Continuation
			if transition == "ask" {
				if question.Status != operations.QuestionPending || question.FallbackAt.Sub(question.AskedAt) != runner.DefaultQuestionTimeout || continuation.DecisionBy != operations.DecisionUser || continuation.Status != operations.ContinuationWaiting || continuation.QuestionID != question.ID {
					t.Fatalf("retry did not suspend the linked decision: question=%+v, continuation=%+v", question, continuation)
				}
			} else if question.Status != operations.QuestionAnswered || question.Answer != answer.Answer || question.AnsweredBy != answer.AnsweredBy || question.AnsweredAt == nil || continuation.DecisionBy != operations.DecisionModel || continuation.Status != operations.ContinuationPending {
				t.Fatalf("retry did not return the answered decision to the model: question=%+v, continuation=%+v", question, continuation)
			}
		})
	}
}

func TestUncertainQuestionWritePreservesDecisionThroughRecovery(t *testing.T) {
	for _, transition := range []string{"ask", "answer"} {
		t.Run(transition, func(t *testing.T) {
			config := fixtures.Config()
			failure := errors.New("directory sync failed")
			uncertain := func(operations.Operation) error { return errors.Join(operations.ErrWriteUncertain, failure) }
			store := &fakes.UncertainWriteStore{OperationStore: config.OperationStore}
			switch transition {
			case "ask":
				store.AfterAsk = uncertain
			case "answer":
				store.AfterAnswer = uncertain
			}
			config.OperationStore = store
			exec, calls := fakes.ControlledExecutor()
			first, err := runner.NewRunner(t.Context(), exec, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = first.Close(ctx)
			})
			before := operationAwaitingDecision(t, first, calls)
			ask := commands.AskQuestionCommand{OperationID: before.ID, TaskID: before.Tasks[0].ID,
				Version: before.Tasks[0].Continuation.Version, Text: "Continue?", Options: []string{"yes", "no"}}
			question, changeErr := first.Ask(t.Context(), ask)
			wantStatus := operations.QuestionPending
			if transition != "ask" {
				if changeErr != nil {
					t.Fatal(changeErr)
				}
				_, changeErr = first.Answer(t.Context(), commands.AnswerQuestionCommand{AnsweredBy: operations.AnswerOperator, OperationID: before.ID, QuestionID: question.ID, Answer: "yes"})
				wantStatus = operations.QuestionAnswered
			}
			stored, err := first.Get(t.Context(), before.ID)
			if err != nil || len(stored.Questions) != 1 || len(stored.Tasks) != 1 || stored.Questions[0].Status != wantStatus {
				t.Fatalf("uncertain %s lost its snapshot: %+v, %v", transition, stored, err)
			}
			savedQuestion, savedDecision := stored.Questions[0], stored.Tasks[0].Continuation
			if savedQuestion.ID == "" || savedQuestion.TaskID != before.Tasks[0].ID || savedQuestion.FallbackAt.IsZero() || savedDecision.QuestionID != savedQuestion.ID {
				t.Fatalf("uncertain %s lost the question identity or deadline: %+v", transition, stored)
			}
			if transition == "answer" {
				if savedQuestion.Answer != "yes" || savedQuestion.AnsweredBy != operations.AnswerOperator || savedQuestion.AnsweredAt == nil {
					t.Fatalf("uncertain answer lost the operator response: %+v", savedQuestion)
				}
			} else if savedQuestion.Answer != "" || savedQuestion.AnsweredBy != "" || savedQuestion.AnsweredAt != nil {
				t.Fatalf("uncertain %s invented an answer: %+v", transition, savedQuestion)
			}
			_, eventErr := first.Events(t.Context(), before.ID, 0, false)
			_, resolveErr := first.Resolve(t.Context(), commands.ResolveContinuationCommand{OperationID: before.ID, TaskID: before.Tasks[0].ID, Version: savedDecision.Version, Actions: []actions.NextAction{}})
			_, retryErr := first.Answer(t.Context(), commands.AnswerQuestionCommand{AnsweredBy: operations.AnswerOperator, OperationID: before.ID, QuestionID: savedQuestion.ID, Answer: "retry"})
			closeErr := first.Close(t.Context())
			for _, err := range []error{changeErr, eventErr, resolveErr, retryErr, closeErr} {
				if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
					t.Fatalf("uncertain %s did not pause the operation: %v", transition, err)
				}
			}

			restarted := fixtures.NewRunner(t, config, exec)
			recovered, err := restarted.Get(t.Context(), before.ID)
			if err != nil || !reflect.DeepEqual(recovered.Questions, stored.Questions) || !reflect.DeepEqual(recovered.Tasks, stored.Tasks) || recovered.Status != stored.Status {
				t.Fatalf("recovery changed the question, deadline, or decision version: %+v, %v", recovered, err)
			}
			if len(recovered.Events) != len(stored.Events)+1 || !reflect.DeepEqual(recovered.Events[:len(stored.Events)], stored.Events) || recovered.Events[len(stored.Events)].Type != operations.OperationRecovered {
				t.Fatalf("recovery lost or repeated a question event: %+v", recovered.Events)
			}
			if transition == "ask" {
				_, err = restarted.Answer(t.Context(), commands.AnswerQuestionCommand{AnsweredBy: operations.AnswerOperator, OperationID: before.ID, QuestionID: savedQuestion.ID, Answer: "yes"})
			} else {
				_, err = restarted.Resolve(t.Context(), commands.ResolveContinuationCommand{OperationID: before.ID, TaskID: before.Tasks[0].ID, Version: savedDecision.Version, Actions: []actions.NextAction{}})
			}
			if err != nil {
				t.Fatalf("recovery did not resume the saved decision: %v", err)
			}
			select {
			case call := <-calls:
				t.Fatalf("question recovery invented follow-up work: %+v", call)
			default:
			}
		})
	}
}

func TestAnswersAfterOperatorWindowResumeLinkedDecision(t *testing.T) {
	for _, origin := range []operations.AnswerOrigin{operations.AnswerOperator, operations.AnswerModel} {
		t.Run(string(origin), func(t *testing.T) {
			config := fixtures.Config()
			config.OperationStore = &fakes.DeadlineOnAnswerStore{OperationStore: config.OperationStore.(*fakes.OperationStore)}
			exec, calls := fakes.ControlledExecutor()
			r := fixtures.NewRunner(t, config, exec)
			before := operationAwaitingDecision(t, r, calls)
			question, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: before.ID, TaskID: before.Tasks[0].ID,
				Version: before.Tasks[0].Continuation.Version, Text: "Continue?"})
			if err != nil {
				t.Fatal(err)
			}

			result, answerErr := r.Answer(t.Context(), commands.AnswerQuestionCommand{AnsweredBy: origin, OperationID: before.ID, QuestionID: question.ID, Answer: "Continue"})
			stored, err := r.Get(t.Context(), before.ID)

			if answerErr != nil || err != nil || !reflect.DeepEqual(result, stored) {
				t.Fatalf("answer after operator window was not saved: operation=%+v, answer=%v, get=%v", result, answerErr, err)
			}
			q, continuation := stored.Questions[0], stored.Tasks[0].Continuation
			if q.ID != question.ID || q.Status != operations.QuestionAnswered || q.Answer != "Continue" || q.AnsweredBy != origin || q.AnsweredAt == nil || q.AnsweredAt.Before(q.FallbackAt) || stored.Status != operations.Planning || continuation.Version != before.Tasks[0].Continuation.Version+2 || continuation.DecisionBy != operations.DecisionModel || continuation.Status != operations.ContinuationPending {
				t.Fatalf("answer after operator window lost provenance or linked decision: %+v", stored)
			}
			events, err := r.Events(t.Context(), before.ID, 0, false)
			if err != nil || !reflect.DeepEqual(events, stored.Events) || len(events) != len(before.Events)+2 || events[len(events)-1].Type != operations.QuestionAnsweredEvent {
				t.Fatalf("answer after operator window did not publish exactly one answer: %+v, %v", events, err)
			}
		})
	}
}

func operationAwaitingDecision(t *testing.T, r *runner.Runner, calls <-chan fakes.Invocation) operations.Operation {
	t.Helper()
	op := fixtures.Start(t, r)
	fixtures.Assign(t, r, op.ID, "inspect")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("inspect")
	return fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
}
