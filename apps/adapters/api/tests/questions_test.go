package api_test

import (
	"net/http"
	"testing"
	"time"

	"commander/adapters/api/models"
)

func TestQuestionsLatestAnswerAndAssignmentBlocking(t *testing.T) {
	a := connect(t)
	url := website(t)
	op := a.start(url)
	base := operationPath(op.ID)
	first := post[models.QuestionResponse](a, base+"/questions", object{"text": "Which release matters?"}, http.StatusCreated)
	created := a.request(http.MethodPost, base+"/questions", object{"text": "Which review matters most?", "options": []string{"Accessibility", "SEO", "Links", "Appearance"}})
	latest := decode[models.QuestionResponse](t, created, http.StatusCreated)
	if created.header.Get("Location") != base+"/questions/"+latest.ID {
		t.Fatal("question Location omitted its saved identity")
	}
	questions := read[models.QuestionListResponse](a, base+"/questions")
	if len(questions.Items) != 2 || questions.LastQuestion == nil || questions.LastQuestion.ID != latest.ID || questions.RemainingQuestions != 1 {
		t.Fatalf("latest question view: %+v", questions)
	}
	if first.Status != "pending" || latest.ExpiresAt.Sub(latest.AskedAt) != 2*time.Minute {
		t.Fatalf("default question lifecycle: %+v", latest)
	}
	assertSchema(t, a, "Question", created)
	assertError(t, a.request(http.MethodPost, base+"/tasks", object{"action_name": "check-seo", "input": object{"url": url}}), http.StatusConflict, "question_pending")
	assertError(t, a.request(http.MethodPost, base+"/complete", object{"output": object{"url": url, "summary": "Unanswered", "findings": []object{}}}), http.StatusConflict, "question_pending")
	answer := "Prioritize keyboard accessibility.\nKeep the review read-only."
	post[models.OperationResponse](a, base+"/questions/"+latest.ID+"/answer", object{"answer": answer}, http.StatusOK)
	saved := read[models.QuestionResponse](a, base+"/questions/"+latest.ID)
	if saved.Answer != answer || saved.Status != "answered" || saved.AnsweredAt == nil {
		t.Fatalf("answer lost operator text: %+v", saved)
	}
	assertError(t, a.request(http.MethodPost, base+"/questions/"+latest.ID+"/answer", object{"answer": "Replace"}), http.StatusConflict, "question_answered")
	questions = read[models.QuestionListResponse](a, base+"/questions")
	if questions.LastQuestion == nil || questions.LastQuestion.ID != first.ID || questions.RemainingQuestions != 0 {
		t.Fatalf("remaining question view: %+v", questions)
	}
	post[models.OperationResponse](a, base+"/questions/"+first.ID+"/answer", object{"answer": "The current release"}, http.StatusOK)
	questions = read[models.QuestionListResponse](a, base+"/questions")
	if questions.LastQuestion != nil || questions.RemainingQuestions != 0 {
		t.Fatalf("answered questions still block: %+v", questions)
	}
	for _, body := range []object{{"text": " "}, {"text": "Choose", "options": []string{"1", "2", "3", "4", "5"}}, {"text": "Choose", "timeout_seconds": -1}, {"text": "Choose", "task_id": absentID}} {
		assertError(t, a.request(http.MethodPost, base+"/questions", body), http.StatusBadRequest, "invalid_request")
	}
	a.complete(op.ID, url, "Answered operator questions")
}

func TestLinkedQuestionsReturnDecisionsAfterAnswerOrExpiry(t *testing.T) {
	for _, expire := range []bool{false, true} {
		name := "answer"
		if expire {
			name = "expiry"
		}
		t.Run(name, func(t *testing.T) {
			a := connect(t)
			url := website(t)
			op := a.start(url)
			a.task(op.ID, "check-seo", url)
			ready := a.wait(op.ID, func(op models.OperationResponse) bool { return modelDecision(op) != nil })
			task := *modelDecision(ready)
			if task.Status != "succeeded" {
				t.Fatalf("real SEO task failed: %+v", task)
			}
			base := operationPath(op.ID)
			question := post[models.QuestionResponse](a, base+"/questions", object{"text": "Is this a release preview?", "options": []string{"Yes", "No"}, "task_id": task.ID, "version": task.Continuation.Version, "timeout_seconds": 3}, http.StatusCreated)
			waiting := read[models.TaskResponse](a, base+"/tasks/"+task.ID)
			if waiting.Continuation.DecisionBy != "user" || waiting.Continuation.Status != "waiting" || waiting.Continuation.QuestionID != question.ID {
				t.Fatalf("linked question did not pause decision: %+v", waiting)
			}
			resolvePath := base + "/tasks/" + task.ID + "/continuation/resolve"
			assertError(t, a.request(http.MethodPost, resolvePath, object{"version": task.Continuation.Version, "actions": []object{}}), http.StatusConflict, "continuation_conflict")
			wantStatus, answer := "expired", ""
			if !expire {
				wantStatus, answer = "answered", "Yes, this is a release preview."
				post[models.OperationResponse](a, base+"/questions/"+question.ID+"/answer", object{"answer": answer}, http.StatusOK)
			}
			resumed := a.wait(op.ID, func(op models.OperationResponse) bool {
				return len(op.Questions) == 1 && op.Questions[0].Status == wantStatus && modelDecision(op) != nil
			})
			decision := modelDecision(resumed)
			if decision.Continuation.Version != task.Continuation.Version+2 || len(resumed.Tasks) != 1 || resumed.Questions[0].Answer != answer || (resumed.Questions[0].AnsweredAt == nil) != expire {
				t.Fatalf("decision handoff invented an answer or work: %+v", resumed)
			}
			code := "question_answered"
			if expire {
				code = "question_expired"
			}
			assertError(t, a.request(http.MethodPost, base+"/questions/"+question.ID+"/answer", object{"answer": "Late replacement"}), http.StatusConflict, code)
			a.resolve(op.ID, *decision, []object{})
			a.complete(op.ID, url, "Reviewed question lifecycle")
		})
	}
}
