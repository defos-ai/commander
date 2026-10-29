package controller_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	controllerfakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	runnerfakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestExecutionQuestionPersistsBeforeReplyAndRetainsFirstWindow(t *testing.T) {
	consumer, executions, store, bus := fixtures.ExecutionQuestionConsumer(t)
	command := executions.Snapshot().Command
	request := fixtures.ExecutionQuestionRequest(t, command, "continue", "Continue this action?", "Yes", "No")
	bus.BeforePublish(func(message messages.Message) {
		stored, err := store.Get(t.Context(), command.Work.OperationID)
		if err != nil || len(stored.Questions) != 1 || message.CorrelationID != request.ID || message.Work != request.Work {
			t.Fatal("question response preceded persistence or lost its request identity")
		}
	})

	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	first := executionQuestionResponse(t, bus)
	if first.Question == nil || first.Error != "" || first.Question.Status != operations.QuestionPending ||
		first.Question.CommandID != command.ID || first.Question.TaskID != command.Work.TaskID || first.Question.Key != "continue" ||
		first.Question.FallbackAt.Sub(first.Question.AskedAt) != operations.DefaultQuestionTimeout {
		t.Fatalf("execution question lost its active scope or operator window: %+v", first)
	}
	before, err := store.Get(t.Context(), command.Work.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Tasks[0].Continuation != nil || before.Tasks[0].Status != operations.TaskRunning {
		t.Fatal("asking changed the running task's continuation or execution state")
	}
	bus.BeforePublish(nil)
	retry := fixtures.ExecutionQuestionRequest(t, command, "continue", "Continue this action?", "Yes", "No")

	if err := consumer.Consume(t.Context(), retry); err != nil {
		t.Fatal(err)
	}
	repeated := executionQuestionResponse(t, bus)
	after, err := store.Get(t.Context(), command.Work.OperationID)
	if err != nil || !reflect.DeepEqual(first.Question, repeated.Question) || !reflect.DeepEqual(before, after) || bus.Messages()[1].CorrelationID != retry.ID {
		t.Fatal("repeated request changed the saved question, window or operation history")
	}
}

func TestExecutionQuestionUsesOperatorFirstAndReturnsAcceptedAnswer(t *testing.T) {
	for _, origin := range []operations.AnswerOrigin{operations.AnswerOperator, operations.AnswerModel} {
		t.Run(string(origin), func(t *testing.T) {
			consumer, executions, store, bus := fixtures.ExecutionQuestionConsumer(t)
			command := executions.Snapshot().Command
			request := fixtures.ExecutionQuestionRequest(t, command, "continue", "Continue this action?", "Yes", "No")
			if err := consumer.Consume(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			question := executionQuestionResponse(t, bus).Question
			before, err := store.Get(t.Context(), command.Work.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.AnswerQuestion(t.Context(), before.ID, question.ID, "Yes", operations.AnswerModel); !errors.Is(err, operations.ErrQuestionOperatorWaiting) {
				t.Fatalf("model bypassed the operator window: %v", err)
			}
			if _, err := store.AnswerQuestion(t.Context(), before.ID, question.ID, strings.Repeat("x", operations.MaxExecutionQuestionAnswerBytes+1), operations.AnswerOperator); err == nil {
				t.Fatal("oversized answer bypassed the execution question response bound")
			}
			retained, err := store.Get(t.Context(), before.ID)
			if err != nil || !reflect.DeepEqual(before, retained) {
				t.Fatal("rejected model answer changed the pending question")
			}
			if origin == operations.AnswerModel {
				if _, err := store.Update(t.Context(), before.ID, func(op *operations.Operation) error {
					op.Questions[0].AskedAt = time.Now().Add(-time.Hour)
					op.Questions[0].FallbackAt = time.Now().Add(-time.Minute)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := store.AnswerQuestion(t.Context(), before.ID, question.ID, "Yes", origin); err != nil {
				t.Fatal(err)
			}
			if err := consumer.Consume(t.Context(), request); err != nil {
				t.Fatal(err)
			}

			answer := executionQuestionResponse(t, bus)
			if answer.Question == nil || answer.Question.Status != operations.QuestionAnswered || answer.Question.Answer != "Yes" || answer.Question.AnsweredBy != origin {
				t.Fatalf("accepted answer did not return to its execution: %+v", answer)
			}
			current, err := store.Get(t.Context(), before.ID)
			if err != nil || current.Tasks[0].Continuation != nil || current.Tasks[0].Status != operations.TaskRunning {
				t.Fatal("answer changed an active task into a model continuation")
			}
		})
	}
}

func TestExecutionQuestionRejectsConflictingKeyWithoutNewQuestion(t *testing.T) {
	consumer, executions, store, bus := fixtures.ExecutionQuestionConsumer(t)
	command := executions.Snapshot().Command
	request := fixtures.ExecutionQuestionRequest(t, command, "continue", "Continue?", "Yes", "No")
	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	before, err := store.Get(t.Context(), command.Work.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	changed := fixtures.ExecutionQuestionRequest(t, command, "continue", "Install something different?", "Yes", "No")

	if err := consumer.Consume(t.Context(), changed); err != nil {
		t.Fatal(err)
	}

	response := executionQuestionResponse(t, bus)
	after, err := store.Get(t.Context(), before.ID)
	if response.Error != agentevents.QuestionConflict || response.Question != nil || err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("changed request reused a key or modified its saved question")
	}
}

func TestExecutionQuestionRequiresActiveSavedOwner(t *testing.T) {
	for _, scenario := range []string{"unknown agent", "wrong work", "finished execution", "finished task", "expired task", "wrong task owner", "terminal operation", "unconfigured store"} {
		t.Run(scenario, func(t *testing.T) {
			consumer, execution, store, bus := fixtures.ExecutionQuestionConsumer(t)
			command := execution.Snapshot().Command
			switch scenario {
			case "unknown agent":
				command.AgentID = "fixture-agent-ffffffff"
			case "wrong work":
				command.Work.TaskID = id.TaskID()
			case "finished execution":
				_, result, _ := fixtures.ArtifactResult(t)
				result.AgentID, result.CorrelationID, result.Work = command.AgentID, command.ID, command.Work
				completed := time.Now().UTC()
				execution.Change(func(record *executions.Execution) { record.Result, record.CompletedAt = &result, &completed })
			case "unconfigured store":
				consumer.Operations = nil
			default:
				if _, err := store.Update(t.Context(), command.Work.OperationID, func(op *operations.Operation) error {
					switch scenario {
					case "finished task":
						op.Tasks[0].Status = operations.TaskFailed
					case "expired task":
						deadline := time.Now().Add(-time.Second)
						op.Tasks[0].DeadlineAt = &deadline
					case "wrong task owner":
						op.Tasks[0].AgentID = "fixture-agent-ffffffff"
					case "terminal operation":
						op.Status = operations.Cancelled
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			request := fixtures.ExecutionQuestionRequest(t, command, "continue", "Continue?")

			err := consumer.Consume(t.Context(), request)

			if scenario == "wrong work" {
				if !errors.Is(err, consumers.ErrInvalidMessage) || len(bus.Messages()) != 0 {
					t.Fatalf("ownership mismatch was not rejected: %v", err)
				}
			} else if err != nil || executionQuestionResponse(t, bus).Error != agentevents.QuestionInactive {
				t.Fatalf("inactive owner accepted a question: %v", err)
			}
			stored, err := store.Get(t.Context(), execution.Snapshot().Command.Work.OperationID)
			if err != nil || len(stored.Questions) != 0 {
				t.Fatal("inactive or invalid owner created a question")
			}
		})
	}
}

func TestExecutionQuestionRecoversUncertainSaveAndLostReply(t *testing.T) {
	for _, scenario := range []string{"uncertain save", "lost reply"} {
		t.Run(scenario, func(t *testing.T) {
			consumer, executions, store, bus := fixtures.ExecutionQuestionConsumer(t)
			command := executions.Snapshot().Command
			request := fixtures.ExecutionQuestionRequest(t, command, "continue", "Continue?")
			failure := errors.New("persistence confirmation unavailable")
			if scenario == "uncertain save" {
				consumer.Operations = &runnerfakes.UncertainWriteStore{OperationStore: store,
					AfterAsk: func(operations.Operation) error { return errors.Join(operations.ErrWriteUncertain, failure) }}
			} else {
				bus.FailPublish(failure)
			}

			if err := consumer.Consume(t.Context(), request); !errors.Is(err, failure) {
				t.Fatalf("lost write/reply confirmation was hidden: %v", err)
			}
			before, err := store.Get(t.Context(), command.Work.OperationID)
			if err != nil || len(before.Questions) != 1 || len(bus.Messages()) != 0 {
				t.Fatal("uncertain write lost the question or acknowledged before confirmation")
			}
			consumer.Operations = store
			bus.FailPublish(nil)
			if err := consumer.Consume(t.Context(), request); err != nil {
				t.Fatal(err)
			}

			response := executionQuestionResponse(t, bus)
			after, err := store.Get(t.Context(), command.Work.OperationID)
			if response.Question == nil || !reflect.DeepEqual(*response.Question, before.Questions[0]) || err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("retry duplicated the question or reset its saved waiting window")
			}
		})
	}
}

func TestExecutionQuestionConfirmsAnswerPersistenceBeforeReturningIt(t *testing.T) {
	consumer, executions, store, bus := fixtures.ExecutionQuestionConsumer(t)
	command := executions.Snapshot().Command
	request := fixtures.ExecutionQuestionRequest(t, command, "continue", "Continue?")
	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	question := executionQuestionResponse(t, bus).Question
	before, err := store.AnswerQuestion(t.Context(), command.Work.OperationID, question.ID, "Yes", operations.AnswerOperator)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("answer durability unconfirmed")
	consumer.Operations = &runnerfakes.UncertainWriteStore{OperationStore: store,
		AfterAsk: func(operations.Operation) error { return errors.Join(operations.ErrWriteUncertain, failure) }}

	err = consumer.Consume(t.Context(), request)

	if !errors.Is(err, failure) || len(bus.Messages()) != 1 {
		t.Fatal("visible answer escaped before its persistence was confirmed")
	}
	consumer.Operations = store
	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	response := executionQuestionResponse(t, bus)
	after, err := store.Get(t.Context(), before.ID)
	if response.Question == nil || response.Question.Answer != "Yes" || err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("confirmed answer changed operation history or failed to return")
	}
}

func TestExecutionQuestionLateAnswerDoesNotResumeFailedTask(t *testing.T) {
	consumer, executions, store, bus := fixtures.ExecutionQuestionConsumer(t)
	command := executions.Snapshot().Command
	request := fixtures.ExecutionQuestionRequest(t, command, "continue", "Continue?")
	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	question := executionQuestionResponse(t, bus).Question
	before, err := store.Update(t.Context(), command.Work.OperationID, func(op *operations.Operation) error {
		op.Tasks[0].Status = operations.TaskFailed
		op.Tasks[0].Continuation = &operations.Continuation{Status: operations.ContinuationPending, DecisionBy: operations.DecisionModel, Version: 4}
		_, err := op.Recover(time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Questions[0], *question) {
		t.Fatal("recovery changed the pending execution question")
	}

	after, err := store.AnswerQuestion(t.Context(), before.ID, question.ID, "Yes", operations.AnswerOperator)

	if err != nil || !reflect.DeepEqual(before.Tasks, after.Tasks) || after.Questions[0].Status != operations.QuestionAnswered {
		t.Fatalf("late answer modified the failed task's independent decision: %v", err)
	}
	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if response := executionQuestionResponse(t, bus); response.Error != agentevents.QuestionInactive || response.Question != nil {
		t.Fatal("late answer was delivered to an ended execution")
	}
}

func executionQuestionResponse(t *testing.T, bus *controllerfakes.SecretPublishBus) agentevents.QuestionResponse {
	t.Helper()
	messages := bus.Messages()
	if len(messages) == 0 {
		t.Fatal("question response was not published")
	}
	response, err := agentevents.DecodeQuestionResponse(messages[len(messages)-1])
	if err != nil {
		t.Fatal(err)
	}
	return response
}
