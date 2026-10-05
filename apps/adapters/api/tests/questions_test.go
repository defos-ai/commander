package api_test

import (
	"bytes"
	"net/http"
	"reflect"
	"testing"
	"time"

	"commander/framework/domain/operations"
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
