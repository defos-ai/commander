package playground_test

import (
	"context"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/chat"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestOperatorAnswersRequestLocalSubmission(t *testing.T) {
	for _, command := range []bool{false, true} {
		name := "question panel"
		if command {
			name = "answer command"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			operation := fixtures.PendingQuestionOperation()
			model := fakes.NewReviewModel(false)
			engine := chat.New(ctx, model, fakes.NewCommander(operation))
			defer engine.Close()
			sendStopCommand(t, engine, "/watch "+operation.ID)
			waitStopEvent(t, ctx, engine, "operation")

			if command {
				sendStopCommand(t, engine, "/answer "+fixtures.QuestionID+" 2")
			} else if err := engine.AnswerQuestion(operation.ID, fixtures.QuestionID, "Decline"); err != nil {
				t.Fatal(err)
			}
			request := waitStopEvent(t, ctx, engine, "answer_request")

			if request.OperationID != operation.ID || request.QuestionID != fixtures.QuestionID || request.Answer != "Decline" {
				t.Fatal("operator answer did not retain its target and selected value for local submission")
			}
			select {
			case <-model.Started():
				t.Fatal("operator submission asked the model to answer")
			default:
			}
		})
	}
}
