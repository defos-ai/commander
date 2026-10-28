package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
)

func TestQuestionsLatestAnswerAndAssignmentBlocking(t *testing.T) {
	a := fixtures.Connect(t)
	op := a.Start(fixtures.FixtureText)
	base := fixtures.OperationPath(op.ID)
	first := fixtures.Post[models.QuestionResponse](a, base+"/questions", fixtures.Object{"text": "Which release matters?"}, http.StatusCreated)
	created := a.Request(http.MethodPost, base+"/questions", fixtures.Object{"text": "Which result matters most?", "options": []string{"Summary", "Words", "Lines", "Characters"}})
	latest := fixtures.Decode[models.QuestionResponse](t, created, http.StatusCreated)
	if created.Header.Get("Location") != base+"/questions/"+latest.ID {
		t.Fatal("question Location omitted its saved identity")
	}
	questions := fixtures.Read[models.QuestionListResponse](a, base+"/questions")
	if len(questions.Items) != 2 || questions.LastQuestion == nil || questions.LastQuestion.ID != latest.ID || questions.RemainingQuestions != 1 {
		t.Fatalf("latest question view: %+v", questions)
	}
	if first.Status != "pending" || latest.ExpiresAt.Sub(latest.AskedAt) != 2*time.Minute {
		t.Fatalf("default question lifecycle: %+v", latest)
	}
	assertSchema(t, a, "Question", created)
	fixtures.AssertError(t, a.Request(http.MethodPost, base+"/tasks", fixtures.Object{"action_name": "analyze-text", "input": fixtures.Object{"text": fixtures.FixtureText}}), http.StatusConflict, "question_pending")
	fixtures.AssertError(t, a.Request(http.MethodPost, base+"/complete", fixtures.Object{"output": fixtures.Object{"summary": "Unanswered"}}), http.StatusConflict, "question_pending")
	answer := "Prioritize the summary.\nKeep the original input."
	fixtures.Post[models.OperationResponse](a, base+"/questions/"+latest.ID+"/answer", fixtures.Object{"answer": answer}, http.StatusOK)
	saved := fixtures.Read[models.QuestionResponse](a, base+"/questions/"+latest.ID)
	if saved.Answer != answer || saved.Status != "answered" || saved.AnsweredAt == nil {
		t.Fatalf("answer lost operator text: %+v", saved)
	}
	fixtures.AssertError(t, a.Request(http.MethodPost, base+"/questions/"+latest.ID+"/answer", fixtures.Object{"answer": "Replace"}), http.StatusConflict, "question_answered")
	questions = fixtures.Read[models.QuestionListResponse](a, base+"/questions")
	if questions.LastQuestion == nil || questions.LastQuestion.ID != first.ID || questions.RemainingQuestions != 0 {
		t.Fatalf("remaining question view: %+v", questions)
	}
	fixtures.Post[models.OperationResponse](a, base+"/questions/"+first.ID+"/answer", fixtures.Object{"answer": "The current release"}, http.StatusOK)
	questions = fixtures.Read[models.QuestionListResponse](a, base+"/questions")
	if questions.LastQuestion != nil || questions.RemainingQuestions != 0 {
		t.Fatalf("answered questions still block: %+v", questions)
	}
	for _, body := range []fixtures.Object{{"text": " "}, {"text": "Choose", "options": []string{"1", "2", "3", "4", "5"}}, {"text": "Choose", "timeout_seconds": -1}, {"text": "Choose", "task_id": fixtures.AbsentID}} {
		fixtures.AssertError(t, a.Request(http.MethodPost, base+"/questions", body), http.StatusBadRequest, "invalid_request")
	}
	a.Complete(op.ID, "Answered operator questions")
}

func TestLinkedQuestionsReturnDecisionsAfterAnswerOrExpiry(t *testing.T) {
	for _, expire := range []bool{false, true} {
		name := "answer"
		if expire {
			name = "expiry"
		}
		t.Run(name, func(t *testing.T) {
			a := fixtures.Connect(t)
			op := a.Start(fixtures.FixtureText)
			a.Task(op.ID, "write-summary", fixtures.SummaryInput())
			ready := a.Wait(op.ID, func(op models.OperationResponse) bool { return fixtures.ModelDecision(op) != nil })
			task := *fixtures.ModelDecision(ready)
			if task.Status != "succeeded" {
				t.Fatalf("summary task failed: %+v", task)
			}
			base := fixtures.OperationPath(op.ID)
			question := fixtures.Post[models.QuestionResponse](a, base+"/questions", fixtures.Object{"text": "Is this a release preview?", "options": []string{"Yes", "No"}, "task_id": task.ID, "version": task.Continuation.Version, "timeout_seconds": 3}, http.StatusCreated)
			waiting := fixtures.Read[models.TaskResponse](a, base+"/tasks/"+task.ID)
			if waiting.Continuation.DecisionBy != "user" || waiting.Continuation.Status != "waiting" || waiting.Continuation.QuestionID != question.ID {
				t.Fatalf("linked question did not pause decision: %+v", waiting)
			}
			resolvePath := base + "/tasks/" + task.ID + "/continuation/resolve"
			fixtures.AssertError(t, a.Request(http.MethodPost, resolvePath, fixtures.Object{"version": task.Continuation.Version, "actions": []fixtures.Object{}}), http.StatusConflict, "continuation_conflict")
			wantStatus, answer := "expired", ""
			if !expire {
				wantStatus, answer = "answered", "Yes, this is a release preview."
				fixtures.Post[models.OperationResponse](a, base+"/questions/"+question.ID+"/answer", fixtures.Object{"answer": answer}, http.StatusOK)
			}
			resumed := a.Wait(op.ID, func(op models.OperationResponse) bool {
				return len(op.Questions) == 1 && op.Questions[0].Status == wantStatus && fixtures.ModelDecision(op) != nil
			})
			decision := fixtures.ModelDecision(resumed)
			if decision.Continuation.Version != task.Continuation.Version+2 || len(resumed.Tasks) != 1 || resumed.Questions[0].Answer != answer || (resumed.Questions[0].AnsweredAt == nil) != expire {
				t.Fatalf("decision handoff invented an answer or work: %+v", resumed)
			}
			code := "question_answered"
			if expire {
				code = "question_expired"
			}
			fixtures.AssertError(t, a.Request(http.MethodPost, base+"/questions/"+question.ID+"/answer", fixtures.Object{"answer": "Late replacement"}), http.StatusConflict, code)
			a.Resolve(op.ID, *decision, []fixtures.Object{})
			a.Complete(op.ID, "Reviewed question lifecycle")
		})
	}
}
