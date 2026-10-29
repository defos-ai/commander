package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestActionQuestionWaitsForItsSavedAnswerAndKeepsRequestIdentity(t *testing.T) {
	returned := make(chan operations.Question, 1)
	detached := make(chan context.Context, 1)
	fixture := fixtures.StartQuestionAgent(t, func(ctx context.Context) error {
		detached <- context.WithoutCancel(ctx)
		ctx = action.WithExecution(ctx, action.Execution{CommandID: id.MessageID(), Work: messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}})
		question, err := action.Question(ctx, "install-source", "Install this agent?", "Yes", "No")
		if err == nil {
			returned <- question
		}
		return err
	})
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}
	commandID := fixture.Command(t, "question", `{}`, work)
	request := fixture.Next(t, controllerevents.QuestionRequested)
	payload, err := controllerevents.DecodeQuestionRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if request.CorrelationID != commandID || payload.CommandID != commandID || request.Work != work {
		t.Fatal("question used public caller metadata instead of its serving execution")
	}
	question := fixtures.PendingExecutionQuestion(t, request)
	pending, err := agentevents.NewQuestionResponse(request, agentevents.QuestionResponse{CommandID: commandID, Key: payload.Key, Question: &question})
	if err != nil {
		t.Fatal(err)
	}
	fixture.Deliver(t, pending)
	now := time.Now().UTC()
	question.Status, question.Answer, question.AnsweredBy, question.AnsweredAt = operations.QuestionAnswered, "Yes", operations.AnswerOperator, &now
	answer, err := agentevents.NewQuestionResponse(request, agentevents.QuestionResponse{CommandID: commandID, Key: payload.Key, Question: &question})
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*messages.Message, *agentevents.QuestionResponse){
		func(m *messages.Message, _ *agentevents.QuestionResponse) { m.AgentID = "other-12345678" },
		func(m *messages.Message, _ *agentevents.QuestionResponse) { m.CorrelationID = id.MessageID() },
		func(m *messages.Message, _ *agentevents.QuestionResponse) { m.Work.TaskID = id.TaskID() },
		func(_ *messages.Message, r *agentevents.QuestionResponse) { r.CommandID = id.MessageID() },
		func(_ *messages.Message, r *agentevents.QuestionResponse) { r.Question.CommandID = id.MessageID() },
		func(_ *messages.Message, r *agentevents.QuestionResponse) { r.Question.Key = "another-question" },
		func(_ *messages.Message, r *agentevents.QuestionResponse) { r.Question.Text = "Something else?" },
		func(_ *messages.Message, r *agentevents.QuestionResponse) {
			r.Question.Options = []string{"Continue", "Stop"}
		},
		func(_ *messages.Message, r *agentevents.QuestionResponse) {
			r.Question.AnsweredBy = operations.AnswerModel
		},
	} {
		message := answer
		response, err := agentevents.DecodeQuestionResponse(answer)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&message, &response)
		message.Payload, err = json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		fixture.Deliver(t, message)
	}

	retried := fixture.Next(t, controllerevents.QuestionRequested)

	select {
	case <-returned:
		t.Fatal("a pending or unrelated response released the action")
	default:
	}
	if !reflect.DeepEqual(retried, request) {
		t.Fatal("polling changed the question request identity or content")
	}
	fixture.Deliver(t, answer)
	fixture.Outcome(t, commandID, controllerevents.ActionSucceeded)
	select {
	case saved := <-returned:
		if !reflect.DeepEqual(saved, question) {
			t.Fatal("action did not receive its saved answer and origin")
		}
	case <-fixture.Context().Done():
		t.Fatal("answered question did not release its action")
	}
	if _, err := action.Question(<-detached, "install-source", "Install this agent?", "Yes", "No"); !errors.Is(err, context.Canceled) {
		t.Fatalf("question capability survived its action: %v", err)
	}
}

func TestActionQuestionWaitEndsWithServingInstance(t *testing.T) {
	returned := make(chan error, 1)
	fixture := fixtures.StartQuestionAgent(t, func(ctx context.Context) error {
		_, err := action.Question(context.WithoutCancel(ctx), "confirm", "Continue?", "Yes", "No")
		returned <- err
		return err
	})
	fixture.Command(t, "question", `{}`, messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()})
	fixture.Next(t, controllerevents.QuestionRequested)

	fixture.Stop(t)

	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("detached question wait did not end with its serving instance: %v", err)
		}
	default:
		t.Fatal("serving instance stopped without awaiting its question waiter")
	}
}

func TestActionQuestionWaitEndsAtTaskDeadlineWithoutController(t *testing.T) {
	returned := make(chan [2]error, 1)
	fixture := fixtures.StartQuestionAgent(t, func(ctx context.Context) error {
		_, err := action.Question(context.WithoutCancel(ctx), "confirm", "Continue?", "Yes", "No")
		returned <- [2]error{err, ctx.Err()}
		return err
	})
	command := fixtures.QuestionCommand(t, fixture.Runtime().ID(), time.Now().Add(time.Second))

	fixture.SendCommand(t, fakes.NewSecretDelivery(command))
	fixture.Next(t, controllerevents.QuestionRequested)
	fixture.Outcome(t, command.ID, controllerevents.CommandFailed)

	select {
	case result := <-returned:
		if !errors.Is(result[0], context.DeadlineExceeded) || result[1] != nil {
			t.Fatalf("question deadline did not bound only its wait: question=%v execution=%v", result[0], result[1])
		}
	case <-fixture.Context().Done():
		t.Fatal("question kept waiting without a controller after its task deadline")
	}
}

func TestStandaloneWorkClearsInheritedQuestionProvider(t *testing.T) {
	executor := fixtures.CheckpointAction(t, func(ctx context.Context) error {
		_, err := action.Question(ctx, "confirm", "Continue?", "Yes", "No")
		if !errors.Is(err, action.ErrQuestionsUnavailable) {
			t.Errorf("standalone action retained a question capability: %v", err)
		}
		return nil
	})
	runtime := fixtures.StateAgent(t, executor)
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	ctx := action.WithQuestions(t.Context(), fakes.QuestionProvider(func(context.Context, string, string, []string) (operations.Question, error) {
		t.Error("standalone work called its caller's question provider")
		return operations.Question{}, nil
	}))

	_, err := runtime.Work(ctx, agent.ActionCall{ActionName: "checkpoint"})

	if err != nil {
		t.Fatal(err)
	}
}
