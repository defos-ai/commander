package tests

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/chat"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestAcceptedQuestionAnswerRefreshesFullStateWithoutRetryingTheMutation(t *testing.T) {
	for _, failRefresh := range []bool{false, true} {
		name := "refresh succeeds"
		if failRefresh {
			name = "watch recovers failed readback"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			commander, operationID := fixtures.NewQuestionCommands(t, failRefresh)
			engine := chat.New(ctx, commander, commander)
			defer engine.Close()

			if err := engine.Send("/watch " + operationID); err != nil {
				t.Fatal(err)
			}

			answered, completed, idle, refreshNotice := false, false, false, false
			for !answered || !completed || !idle || failRefresh && !refreshNotice {
				select {
				case event, ok := <-engine.Events():
					if !ok {
						t.Fatal("engine stopped before the accepted answer was refreshed")
					}
					switch event.Kind {
					case "error":
						t.Fatalf("accepted answer was reported as failed: %s", event.Text)
					case "success":
						completed = completed || event.Text == "answer_question completed"
					case "notice":
						refreshNotice = refreshNotice || strings.Contains(event.Text, "Answer accepted; operation refresh unavailable")
					case "idle":
						idle = true
					case "operation":
						operation := event.Operation
						if operation == nil || operation.ID != operationID || len(operation.Tasks) != 1 || len(operation.Questions) != 1 || !bytes.Contains(operation.Raw, []byte(`"question-fixture"`)) {
							t.Fatal("a compact command receipt replaced the full watched operation")
						}
						question := operation.Questions[0]
						if question.Status == bridge.QuestionAnswered {
							if question.Answer != "Summary" || question.AnsweredBy != "model" || operation.Progress.PendingQuestions != 0 {
								t.Fatal("refreshed operation lost the saved answer or its model origin")
							}
							answered = true
						}
					}
				case <-ctx.Done():
					t.Fatalf("accepted answer did not produce a full snapshot: answered=%t completed=%t idle=%t refreshNotice=%t", answered, completed, idle, refreshNotice)
				}
			}
			engine.Close()
			answers, reads, modelResult := commander.Snapshot()
			if answers != 1 || reads == 0 || !strings.Contains(modelResult, `"id":"`+operationID+`"`) || strings.Contains(modelResult, "failed") {
				t.Fatalf("readback changed the accepted mutation: answers=%d fresh reads=%d model result=%q", answers, reads, modelResult)
			}
			if failRefresh && reads < 2 {
				t.Fatal("background watching did not recover the failed operation readback")
			}
		})
	}
}
