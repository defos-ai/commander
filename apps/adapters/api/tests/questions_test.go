package api_test

import (
	"bytes"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/framework/domain/operations"
	"commander/framework/id"
)

func TestOperationQuestionsPreserveHistoryAndSummarizePendingQuestions(t *testing.T) {
	fixture := newFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	op := operationRecord(1)
	op.Status = operations.Waiting
	op.Questions = []operations.Question{
		{ID: recordID(10), Status: operations.QuestionAnswered, StepID: "review-links", TaskID: recordID(40), Text: "Include links?", Options: []string{"yes", "no"}, Answer: "yes", AskedAt: now.Add(-time.Minute), AnsweredAt: &now, ExpiresAt: now.Add(time.Hour)},
		{ID: recordID(30), Status: operations.QuestionPending, Text: "Review external links?", Options: []string{"yes", "no"}, AskedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(time.Hour)},
		{ID: recordID(50), Status: operations.QuestionPending, Text: "Overdue question", AskedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute)},
		{ID: recordID(20), Status: operations.QuestionPending, Text: "Review images?", AskedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(time.Hour)},
		{ID: recordID(60), Status: operations.QuestionPending, Text: "Question without a deadline", AskedAt: now.Add(-3 * time.Hour)},
		{ID: recordID(70), Status: operations.QuestionExpired, Text: "Already expired", AskedAt: now, ExpiresAt: now.Add(-time.Minute)},
	}
	op = saveOperation(t, operationStore(t, fixture), op)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID + "/questions"
	before := operationBytes(t, fixture, op.ID)

	list := decodeResponse[questionList](t, server.request(t, http.MethodGet, base, authenticate))
	answered := decodeResponse[operations.Question](t, server.request(t, http.MethodGet, base+"/"+op.Questions[0].ID, authenticate))
	overdue := decodeResponse[operations.Question](t, server.request(t, http.MethodGet, base+"/"+op.Questions[2].ID, authenticate))

	if !reflect.DeepEqual(list.Items, op.Questions) {
		t.Fatalf("question history changed saved records or their order: got %+v, want %+v", list.Items, op.Questions)
	}
	if list.LastQuestion == nil || !reflect.DeepEqual(*list.LastQuestion, op.Questions[1]) || list.RemainingQuestions != 2 {
		t.Fatalf("summary must show latest eligible question, break ties by highest ID, and exclude it from remaining: %+v", list)
	}
	if !reflect.DeepEqual(answered, op.Questions[0]) || !reflect.DeepEqual(overdue, op.Questions[2]) || overdue.Status != operations.QuestionPending {
		t.Fatalf("question details must return complete saved records, including overdue pending status: answered=%+v overdue=%+v", answered, overdue)
	}
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("question queries changed persisted state or expired an overdue question")
	}
}

func TestTerminalOperationsHidePendingQuestionSummary(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	server := startAPI(t, fixture.localArgs())
	now := time.Now().UTC().Truncate(time.Second)
	for i, status := range []operations.Status{operations.Completed, operations.Failed, operations.Cancelled} {
		t.Run(string(status), func(t *testing.T) {
			op := operationRecord(i + 1)
			op.Status = status
			op.Questions = []operations.Question{
				{ID: recordID(10), Status: operations.QuestionPending, Text: "Continue review?", AskedAt: now, ExpiresAt: now.Add(time.Hour)},
				{ID: recordID(20), Status: operations.QuestionPending, Text: "Overdue question", AskedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute)},
			}
			op = saveOperation(t, store, op)
			before := operationBytes(t, fixture, op.ID)

			list := decodeResponse[questionList](t, server.request(t, http.MethodGet, "/api/v1/operations/"+op.ID+"/questions", authenticate))

			if !reflect.DeepEqual(list.Items, op.Questions) || list.LastQuestion != nil || list.RemainingQuestions != 0 {
				t.Fatalf("terminal operation must retain history with no active question summary: %+v", list)
			}
			if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
				t.Fatal("question query changed terminal operation storage")
			}
		})
	}
}

type questionList struct {
	Items              []operations.Question `json:"items"`
	LastQuestion       *operations.Question  `json:"last_question"`
	RemainingQuestions int                   `json:"remaining_questions"`
}

func TestOperationQuestionCommandsPersistQuestionsAndAnswers(t *testing.T) {
	fixture := newFixture(t)
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	op := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(`{"mission_name":"review","input":{"url":"https://example.test"}}`, "application/json")), http.StatusCreated)
	base := "/api/v1/operations/" + op.ID
	body := `{"text":"Which links should we review?","options":["Internal","External","Both","Neither"],"step_id":"links","timeout_seconds":3600}`

	created := server.request(t, http.MethodPost, base+"/questions", authenticate, commandBody(body, "application/json; charset=utf-8"))
	question := decodeResponse[operations.Question](t, created, http.StatusCreated)
	saved := decodeResponse[operations.Question](t, server.request(t, http.MethodGet, base+"/questions/"+question.ID, authenticate))

	if !id.Valid(question.ID) || created.header.Get("Location") != base+"/questions/"+question.ID || !reflect.DeepEqual(question, saved) {
		t.Fatalf("ask must return the full saved question and its location: location=%q question=%+v saved=%+v", created.header.Get("Location"), question, saved)
	}
	if question.Status != operations.QuestionPending || question.Text != "Which links should we review?" || question.StepID != "links" || question.TaskID != "" || question.AskedAt.IsZero() || question.ExpiresAt.Sub(question.AskedAt) != time.Hour || question.Answer != "" || question.AnsweredAt != nil || !reflect.DeepEqual(question.Options, []string{"Internal", "External", "Both", "Neither"}) {
		t.Fatalf("ask lost question fields or lifecycle: %+v", question)
	}
	const answer = "Review external links first.\nPreserve the original scope."
	answered := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, base+"/questions/"+question.ID+"/answer", authenticate, commandBody(fmt.Sprintf(`{"answer":%q}`, answer), "application/json")))
	stored := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))

	if !reflect.DeepEqual(answered, stored) || len(answered.Questions) != 1 || len(answered.Tasks) != 0 {
		t.Fatalf("answer must return the full saved operation without assigning tasks: response=%+v saved=%+v", answered, stored)
	}
	recorded := answered.Questions[0]
	if recorded.ID != question.ID || recorded.Status != operations.QuestionAnswered || recorded.Answer != answer || recorded.AnsweredAt == nil || recorded.AnsweredAt.Before(question.AskedAt) || !recorded.ExpiresAt.Equal(question.ExpiresAt) || !reflect.DeepEqual(recorded.Options, question.Options) {
		t.Fatalf("answer did not preserve the question and record the free-text answer: %+v", recorded)
	}
	if len(answered.Events) != 3 || answered.Events[1].Type != operations.QuestionAsked || answered.Events[1].QuestionID != question.ID || answered.Events[2].Type != operations.QuestionAnsweredEvent || answered.Events[2].QuestionID != question.ID {
		t.Fatalf("question commands lost their persisted events: %+v", answered.Events)
	}
	before := operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodPost, base+"/questions/"+question.ID+"/answer", authenticate, commandBody(`{"answer":"Replace the answer"}`, "application/json")), http.StatusConflict, "question_answered")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("answer replay changed the stored answer or operation revision")
	}
}

func TestOperationQuestionTimeoutDefaultsAndBounds(t *testing.T) {
	fixture := newFixture(t)
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	op := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, "/api/v1/operations", authenticate, commandBody(`{"mission_name":"any-input","input":{}}`, "application/json")), http.StatusCreated)
	for _, tc := range []struct {
		name    string
		field   string
		seconds int
	}{{"omitted", "", 120}, {"zero", `,"timeout_seconds":0`, 120}, {"minimum", `,"timeout_seconds":1`, 1}, {"maximum", `,"timeout_seconds":86400`, 86400}} {
		t.Run(tc.name, func(t *testing.T) {
			question := decodeResponse[operations.Question](t, server.request(t, http.MethodPost, "/api/v1/operations/"+op.ID+"/questions", authenticate, commandBody(`{"text":"Continue?"`+tc.field+`}`, "application/json")), http.StatusCreated)

			if question.ExpiresAt.Sub(question.AskedAt) != time.Duration(tc.seconds)*time.Second {
				t.Fatalf("question deadline ignored timeout: %+v", question)
			}
		})
	}
}

func TestOperationQuestionCommandsRejectInvalidRequestsWithoutMutation(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), continuationOperationRecord(1))
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID + "/questions"
	answerPath := base + "/" + recordID(30) + "/answer"
	before := operationBytes(t, fixture, op.ID)

	for _, path := range []string{base, answerPath} {
		assertAPIError(t, server.request(t, http.MethodPost, path), http.StatusUnauthorized, "authentication_required")
		for _, body := range []string{``, `{`, `{} {}`, `null`, `[]`, `"text"`, `{"unknown":true}`} {
			assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_request")
		}
		assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(`{}`, "text/plain")), http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	for _, body := range []string{
		`{}`, `{"text":" "}`, `{"text":17}`, `{"text":"Continue?","options":["yes"," "]}`, `{"text":"Continue?","options":[1]}`,
		`{"text":"Continue?","options":["1","2","3","4","5"]}`, `{"text":"Continue?","timeout_seconds":-1}`,
		`{"text":"Continue?","timeout_seconds":86401}`, `{"text":"Continue?","timeout_seconds":1.5}`, `{"text":"Continue?","step_id":17}`,
		`{"text":"Continue?","version":2}`, `{"text":"Continue?","task_id":"invalid","version":2}`,
		fmt.Sprintf(`{"text":"Continue?","task_id":%q}`, op.Tasks[0].ID),
		fmt.Sprintf(`{"text":"Continue?","task_id":%q,"version":0}`, op.Tasks[0].ID),
		fmt.Sprintf(`{"text":"Continue?","task_id":%q,"version":-1}`, op.Tasks[0].ID),
		fmt.Sprintf(`{"text":"Continue?","operation_id":%q}`, op.ID), `{"text":"Continue?","agent_id":"review-agent-1234abcd"}`,
	} {
		assertAPIError(t, server.request(t, http.MethodPost, base, authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_request")
	}
	for _, body := range []string{`{}`, `{"answer":" \n"}`, `{"answer":17}`, `{"answer":null}`, `{"answer":"yes","question_id":"ignored"}`, `{"answer":"yes","operation_id":"ignored"}`, `{"answer":"yes","agent_id":"review-agent-1234abcd"}`} {
		assertAPIError(t, server.request(t, http.MethodPost, answerPath, authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_request")
	}
	for _, invalid := range []string{"not-a-uuid", strings.ToUpper(recordID(0xabcdef)), strings.ReplaceAll(op.ID, "-", "")} {
		assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations/"+invalid+"/questions", authenticate, commandBody(`{"text":"Continue?"}`, "application/json")), http.StatusBadRequest, "invalid_operation_id")
		assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations/"+invalid+"/questions/"+recordID(30)+"/answer", authenticate, commandBody(`{"answer":"yes"}`, "application/json")), http.StatusBadRequest, "invalid_operation_id")
		assertAPIError(t, server.request(t, http.MethodPost, base+"/"+invalid+"/answer", authenticate, commandBody(`{"answer":"yes"}`, "application/json")), http.StatusBadRequest, "invalid_question_id")
	}

	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("invalid question commands changed the operation or its revision")
	}
}

func TestOperationQuestionCommandsReturnControllerDomainErrors(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), continuationOperationRecord(1))
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID
	missing := "/api/v1/operations/" + recordID(999)
	before := operationBytes(t, fixture, op.ID)

	assertAPIError(t, server.request(t, http.MethodPost, missing+"/questions", authenticate, commandBody(`{"text":"Continue?"}`, "application/json")), http.StatusNotFound, "operation_not_found")
	assertAPIError(t, server.request(t, http.MethodPost, missing+"/questions/"+recordID(30)+"/answer", authenticate, commandBody(`{"answer":"yes"}`, "application/json")), http.StatusNotFound, "operation_not_found")
	assertAPIError(t, server.request(t, http.MethodPost, base+"/questions/"+recordID(30)+"/answer", authenticate, commandBody(`{"answer":"yes"}`, "application/json")), http.StatusNotFound, "question_not_found")
	assertAPIError(t, server.request(t, http.MethodPost, base+"/questions", authenticate, commandBody(fmt.Sprintf(`{"text":"Continue?","task_id":%q,"version":2}`, recordID(999)), "application/json")), http.StatusNotFound, "task_not_found")
	assertAPIError(t, server.request(t, http.MethodPost, base+"/questions", authenticate, commandBody(fmt.Sprintf(`{"text":"Continue?","task_id":%q,"version":1}`, op.Tasks[0].ID), "application/json")), http.StatusConflict, "continuation_conflict")
	assertAPIError(t, server.request(t, http.MethodPost, base+"/questions", authenticate, commandBody(`{"text":"Continue?","step_id":"missing-step"}`, "application/json")), http.StatusUnprocessableEntity, "invalid_input")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("rejected question commands changed persisted state")
	}
	decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, base+"/cancel", authenticate))
	before = operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodPost, base+"/questions", authenticate, commandBody(`{"text":"Continue?"}`, "application/json")), http.StatusConflict, "operation_finished")
	assertAPIError(t, server.request(t, http.MethodPost, base+"/questions/"+recordID(30)+"/answer", authenticate, commandBody(`{"answer":"yes"}`, "application/json")), http.StatusConflict, "operation_finished")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("question commands changed a finished operation")
	}
}

func TestOperationQuestionLateAnswerPersistsExpirationAndReturnsDecision(t *testing.T) {
	fixture := newFixture(t)
	store := operationStore(t, fixture)
	op := saveOperation(t, store, continuationOperationRecord(1))
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID
	question := decodeResponse[operations.Question](t, server.request(t, http.MethodPost, base+"/questions", authenticate, commandBody(fmt.Sprintf(`{"text":"Continue?","task_id":%q,"version":2}`, op.Tasks[0].ID), "application/json")), http.StatusCreated)
	// Move the persisted deadline into the past so the answer path must save expiration.
	if _, err := store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		next.Questions[0].AskedAt = time.Now().UTC().Add(-2 * time.Minute)
		next.Questions[0].ExpiresAt = time.Now().UTC().Add(-time.Minute)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	result := server.request(t, http.MethodPost, base+"/questions/"+question.ID+"/answer", authenticate, commandBody(`{"answer":"Too late"}`, "application/json"))
	saved := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))

	assertAPIError(t, result, http.StatusConflict, "question_expired")
	q, continuation := saved.Questions[0], saved.Tasks[0].Continuation
	if q.Status != operations.QuestionExpired || q.Answer != "" || q.AnsweredAt != nil || len(saved.Tasks) != 1 || continuation.Version != 4 || continuation.DecisionBy != operations.DecisionModel || continuation.Status != operations.ContinuationPending {
		t.Fatalf("late answer did not persist expiration and return the decision without children: %+v", saved)
	}
	if last := saved.Events[len(saved.Events)-1]; last.Type != operations.QuestionExpiredEvent || last.QuestionID != question.ID || last.TaskID != op.Tasks[0].ID {
		t.Fatalf("late answer lost the expiration event: %+v", saved.Events)
	}
	before := operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodPost, base+"/questions/"+question.ID+"/answer", authenticate, commandBody(`{"answer":"Retry"}`, "application/json")), http.StatusConflict, "question_expired")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("late answer retry changed the expiration event or continuation version")
	}
}
