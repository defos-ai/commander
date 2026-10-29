package runner_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestQuestionAnswerRetainsFirstOrigin(t *testing.T) {
	for _, origin := range []operations.AnswerOrigin{operations.AnswerOperator, operations.AnswerModel} {
		t.Run(string(origin), func(t *testing.T) {
			config := fixtures.Config()
			if origin == operations.AnswerModel {
				config.OperationStore = &fakes.DeadlineOnAnswerStore{OperationStore: config.OperationStore.(*fakes.OperationStore)}
			}
			exec, _ := fakes.ControlledExecutor()
			r := fixtures.NewRunner(t, config, exec)
			op := fixtures.Start(t, r)
			question, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, Text: "Continue?"})
			if err != nil {
				t.Fatal(err)
			}

			answered, err := r.Answer(t.Context(), commands.AnswerQuestionCommand{
				OperationID: op.ID, QuestionID: question.ID, Answer: "yes", AnsweredBy: origin,
			})

			if err != nil || len(answered.Questions) != 1 || answered.Questions[0].AnsweredBy != origin ||
				answered.Questions[0].Status != operations.QuestionAnswered || answered.Questions[0].Answer != "yes" {
				t.Fatalf("answer lost its origin: %+v, %v", answered, err)
			}
			other := operations.AnswerOperator
			if origin == other {
				other = operations.AnswerModel
			}

			_, replacementErr := r.Answer(t.Context(), commands.AnswerQuestionCommand{
				OperationID: op.ID, QuestionID: question.ID, Answer: "no", AnsweredBy: other,
			})
			retained, err := r.Get(t.Context(), op.ID)

			if !errors.Is(replacementErr, operations.ErrQuestionAnswered) || err != nil || !reflect.DeepEqual(retained, answered) {
				t.Fatalf("another origin replaced the accepted answer: %+v, replacement=%v, get=%v", retained, replacementErr, err)
			}
		})
	}
}

func TestQuestionAnswerRequiresValidOrigin(t *testing.T) {
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
	for _, origin := range []operations.AnswerOrigin{"", "external"} {
		t.Run("origin="+string(origin), func(t *testing.T) {
			command := commands.AnswerQuestionCommand{OperationID: op.ID, QuestionID: question.ID, Answer: "yes", AnsweredBy: origin}
			next := before.Clone()

			commandErr := command.Validate()
			domainErr := next.AnswerQuestion(question.ID, command.Answer, origin)

			if commandErr == nil || domainErr == nil || !reflect.DeepEqual(next, before) {
				t.Fatal("missing or unknown answer origin was accepted or mutated the pending question")
			}
		})
	}
}

func TestPendingQuestionCannotClaimAnswerOrigin(t *testing.T) {
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
	for _, origin := range []operations.AnswerOrigin{operations.AnswerOperator, operations.AnswerModel} {
		t.Run(string(origin), func(t *testing.T) {
			pending := question.Clone()
			pending.ID, pending.AnsweredBy = id.QuestionID(), origin
			next := before.Clone()

			err := next.AskQuestion(pending, 0)

			if err == nil || !reflect.DeepEqual(next, before) {
				t.Fatal("a pending question claimed an answer origin")
			}
		})
	}
}
