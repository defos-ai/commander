package runner_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestQuestionWriteFailurePreservesLinkedDecision(t *testing.T) {
	for _, transition := range []string{"ask", "answer"} {
		t.Run(transition, func(t *testing.T) {
			config := fixture()
			exec, calls := controlledExecutor()
			r := newRunner(t, config, exec)
			before := operationAwaitingDecision(t, r, calls)
			ask := commands.AskQuestionCommand{OperationID: before.ID, TaskID: before.Tasks[0].ID,
				Version: before.Tasks[0].Continuation.Version, Text: "Continue?", Options: []string{"yes", "no"}}
			answer := commands.AnswerQuestionCommand{OperationID: before.ID, Answer: "yes"}
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
			config.OperationStore.(*operationStore).failNextUpdate(failure)

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
				if question.Status != operations.QuestionPending || question.ExpiresAt.Sub(question.AskedAt) != runner.DefaultQuestionTimeout || continuation.DecisionBy != operations.DecisionUser || continuation.Status != operations.ContinuationWaiting || continuation.QuestionID != question.ID {
					t.Fatalf("retry did not suspend the linked decision: question=%+v, continuation=%+v", question, continuation)
				}
			} else if question.Status != operations.QuestionAnswered || question.Answer != answer.Answer || question.AnsweredAt == nil || continuation.DecisionBy != operations.DecisionModel || continuation.Status != operations.ContinuationPending {
				t.Fatalf("retry did not return the answered decision to the model: question=%+v, continuation=%+v", question, continuation)
			}
		})
	}
}

func TestUncertainQuestionWritePreservesDecisionThroughRecovery(t *testing.T) {
	for _, transition := range []string{"ask", "answer", "expire"} {
		t.Run(transition, func(t *testing.T) {
			config := fixture()
			failure := errors.New("directory sync failed")
			uncertain := func(operations.Operation) error { return errors.Join(operations.ErrWriteUncertain, failure) }
			store := &uncertainWriteStore{OperationStore: config.OperationStore}
			switch transition {
			case "ask":
				store.afterAsk = uncertain
			case "answer":
				store.afterAnswer = uncertain
			case "expire":
				store.afterExpire = uncertain
			}
			config.OperationStore = store
			exec, calls := controlledExecutor()
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
			if transition == "expire" {
				ask.TimeoutSeconds = 1
			}
			question, changeErr := first.Ask(t.Context(), ask)
			wantStatus := operations.QuestionPending
			if transition != "ask" {
				if changeErr != nil {
					t.Fatal(changeErr)
				}
				if transition == "answer" {
					_, changeErr = first.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: before.ID, QuestionID: question.ID, Answer: "yes"})
					wantStatus = operations.QuestionAnswered
				} else {
					awaitOperation(t, first, before.ID, func(op operations.Operation) bool { return op.Questions[0].Status == operations.QuestionExpired })
					_, changeErr = first.Events(t.Context(), before.ID, 0, false)
					wantStatus = operations.QuestionExpired
				}
			}
			stored, err := first.Get(t.Context(), before.ID)
			if err != nil || len(stored.Questions) != 1 || len(stored.Tasks) != 1 || stored.Questions[0].Status != wantStatus {
				t.Fatalf("uncertain %s lost its snapshot: %+v, %v", transition, stored, err)
			}
			savedQuestion, savedDecision := stored.Questions[0], stored.Tasks[0].Continuation
			if savedQuestion.ID == "" || savedQuestion.TaskID != before.Tasks[0].ID || savedQuestion.ExpiresAt.IsZero() || savedDecision.QuestionID != savedQuestion.ID {
				t.Fatalf("uncertain %s lost the question identity or deadline: %+v", transition, stored)
			}
			if transition == "answer" {
				if savedQuestion.Answer != "yes" || savedQuestion.AnsweredAt == nil {
					t.Fatalf("uncertain answer lost the operator response: %+v", savedQuestion)
				}
			} else if savedQuestion.Answer != "" || savedQuestion.AnsweredAt != nil {
				t.Fatalf("uncertain %s invented an answer: %+v", transition, savedQuestion)
			}
			_, eventErr := first.Events(t.Context(), before.ID, 0, false)
			_, resolveErr := first.Resolve(t.Context(), commands.ResolveContinuationCommand{OperationID: before.ID, TaskID: before.Tasks[0].ID, Version: savedDecision.Version, Actions: []actions.NextAction{}})
			_, retryErr := first.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: before.ID, QuestionID: savedQuestion.ID, Answer: "retry"})
			closeErr := first.Close(t.Context())
			for _, err := range []error{changeErr, eventErr, resolveErr, retryErr, closeErr} {
				if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
					t.Fatalf("uncertain %s did not pause the operation: %v", transition, err)
				}
			}

			restarted := newRunner(t, config, exec)
			recovered, err := restarted.Get(t.Context(), before.ID)
			if err != nil || !reflect.DeepEqual(recovered.Questions, stored.Questions) || !reflect.DeepEqual(recovered.Tasks, stored.Tasks) || recovered.Status != stored.Status {
				t.Fatalf("recovery changed the question, deadline, or decision version: %+v, %v", recovered, err)
			}
			if len(recovered.Events) != len(stored.Events)+1 || !reflect.DeepEqual(recovered.Events[:len(stored.Events)], stored.Events) || recovered.Events[len(stored.Events)].Type != operations.OperationRecovered {
				t.Fatalf("recovery lost or repeated a question event: %+v", recovered.Events)
			}
			if transition == "ask" {
				_, err = restarted.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: before.ID, QuestionID: savedQuestion.ID, Answer: "yes"})
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

func TestLateAnswerPersistsExpirationBeforeReturningError(t *testing.T) {
	config := fixture()
	config.OperationStore = &deadlineOnAnswerStore{operationStore: config.OperationStore.(*operationStore)}
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	before := operationAwaitingDecision(t, r, calls)
	question, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: before.ID, TaskID: before.Tasks[0].ID,
		Version: before.Tasks[0].Continuation.Version, Text: "Continue?"})
	if err != nil {
		t.Fatal(err)
	}

	result, answerErr := r.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: before.ID, QuestionID: question.ID, Answer: "too late"})
	stored, err := r.Get(t.Context(), before.ID)
	if answerErr == nil || !strings.Contains(answerErr.Error(), "question has expired") || err != nil || !reflect.DeepEqual(result, stored) {
		t.Fatalf("late answer did not return the durable expired snapshot: operation=%+v, answer=%v, get=%v", result, answerErr, err)
	}
	q, continuation := stored.Questions[0], stored.Tasks[0].Continuation
	if q.ID != question.ID || q.Status != operations.QuestionExpired || q.Answer != "" || q.AnsweredAt != nil || stored.Status != operations.Planning || continuation.Version != before.Tasks[0].Continuation.Version+2 || continuation.DecisionBy != operations.DecisionModel || continuation.Status != operations.ContinuationPending {
		t.Fatalf("late answer lost expiration or changed the decision incorrectly: %+v", stored)
	}
	events, err := r.Events(t.Context(), before.ID, 0, false)
	if err != nil || !reflect.DeepEqual(events, stored.Events) || len(events) != len(before.Events)+2 || events[len(events)-1].Type != operations.QuestionExpiredEvent {
		t.Fatalf("late answer did not publish exactly one expiration: %+v, %v", events, err)
	}
}

func operationAwaitingDecision(t *testing.T, r *runner.Runner, calls <-chan invocation) operations.Operation {
	t.Helper()
	op := start(t, r)
	assign(t, r, op.ID, "inspect")
	receive(t, calls).reply <- output("inspect")
	return awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
}

// Expire the durable deadline within AnswerQuestion so the runner's periodic
// sweep cannot handle expiration before the answer reaches the store.
type deadlineOnAnswerStore struct{ *operationStore }

func (s *deadlineOnAnswerStore) AnswerQuestion(ctx context.Context, operationID, questionID, answer string) (operations.Operation, error) {
	if _, err := s.operationStore.Update(ctx, operationID, func(op *operations.Operation) error {
		for i := range op.Questions {
			if op.Questions[i].ID == questionID {
				op.Questions[i].ExpiresAt = time.Now().Add(-time.Second)
			}
		}
		return nil
	}); err != nil {
		return operations.Operation{}, err
	}
	return s.operationStore.AnswerQuestion(ctx, operationID, questionID, answer)
}
