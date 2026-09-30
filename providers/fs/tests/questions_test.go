package providers_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"commander/framework/domain/operations"
	"commander/framework/id"
	operationfs "commander/providers/fs/operations"
)

func TestOperationQuestionAskAndAnswerAreDurable(t *testing.T) {
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op, _ := createContinuationOperation(t, store)
	question := newStoreQuestion(op.Tasks[0].ID)
	started := time.Now().UTC()
	asked, err := store.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version)
	if err != nil {
		t.Fatal(err)
	}
	expected := question
	expected.StepID = op.Tasks[0].StepID
	if len(asked.Questions) != 1 || asked.Status != operations.Waiting {
		t.Fatalf("ask did not publish the pending question and waiting status: %+v", asked)
	}
	assertResultJSONEqual(t, asked.Questions[0], expected)
	assertQuestionContinuation(t, asked, operations.DecisionUser, operations.ContinuationWaiting, question.ID, op.Tasks[0].Continuation.Version+1)
	assertQuestionEvent(t, asked, operations.QuestionAsked, question.ID, len(op.Events)+1, started)
	assertQuestionRejected(t, dir, op.ID, func() (operations.Operation, error) {
		return store.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version)
	}, operations.ErrOperationConflict)

	path := filepath.Join(dir, "operations", op.ID+".json")
	before := readDispatchFile(t, path)
	unchanged, err := store.ExpireQuestions(t.Context(), op.ID, question.ExpiresAt.Add(-time.Nanosecond))
	if err != nil || !bytes.Equal(before, readDispatchFile(t, path)) {
		t.Fatalf("sweep before deadline rewrote the operation: %v", err)
	}
	assertResultJSONEqual(t, unchanged, asked)
	// Neither caller options nor returned snapshots may mutate the saved envelope.
	question.Options[0] = "changed by caller"
	asked.Questions[0].Options[1] = "changed by reader"
	answered, err := newDispatchStore(t, dir).AnswerQuestion(t.Context(), op.ID, question.ID, "Proceed with review")
	if err != nil {
		t.Fatal(err)
	}
	q := answered.Questions[0]
	if q.Status != operations.QuestionAnswered || q.Answer != "Proceed with review" || q.AnsweredAt == nil || q.AnsweredAt.Before(started) || !q.AnsweredAt.Before(q.ExpiresAt) || answered.Status != operations.Planning {
		t.Fatalf("answer did not publish the answer, time, and resumed status: %+v", answered)
	}
	if q.Options[0] != "yes" || q.Options[1] != "no" || !q.AskedAt.Equal(expected.AskedAt) || !q.ExpiresAt.Equal(expected.ExpiresAt) {
		t.Fatalf("saved question envelope changed: %+v", q)
	}
	assertQuestionContinuation(t, answered, operations.DecisionModel, operations.ContinuationPending, question.ID, op.Tasks[0].Continuation.Version+2)
	assertQuestionEvent(t, answered, operations.QuestionAnsweredEvent, question.ID, len(op.Events)+2, started)
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, answered)
	assertQuestionRejected(t, dir, op.ID, func() (operations.Operation, error) {
		return store.AnswerQuestion(t.Context(), op.ID, question.ID, "replacement")
	}, nil)
}

func TestOperationQuestionCompetingAsksUseLatestContinuation(t *testing.T) {
	dir := t.TempDir()
	first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op, _ := createContinuationOperation(t, first)
	type result struct {
		question operations.Question
		op       operations.Operation
		err      error
	}
	gate, results := make(chan struct{}), make(chan result, 2)
	for _, store := range []*operationfs.OperationFsStore{first, second} {
		question := newStoreQuestion(op.Tasks[0].ID)
		go func() {
			<-gate
			published, err := store.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version)
			results <- result{question, published, err}
		}()
	}
	close(gate)
	a, b := <-results, <-results
	if a.err != nil {
		a, b = b, a
	}
	if a.err != nil || b.err == nil || b.op.ID != "" || len(a.op.Questions) != 1 || a.op.Questions[0].ID != a.question.ID || countResultEvents(a.op, operations.QuestionAsked) != 1 {
		t.Fatalf("competing asks did not produce exactly one question: %+v; %+v", a, b)
	}
	assertQuestionContinuation(t, a.op, operations.DecisionUser, operations.ContinuationWaiting, a.question.ID, op.Tasks[0].Continuation.Version+1)
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, a.op)
}

func TestOperationQuestionRejectsInvalidEnvelope(t *testing.T) {
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op, _ := createContinuationOperation(t, store)
	for _, tc := range []struct {
		name   string
		change func(*operations.Question)
	}{
		{"question ID", func(q *operations.Question) { q.ID = "invalid" }},
		{"task ID", func(q *operations.Question) { q.TaskID = "invalid" }},
		{"status", func(q *operations.Question) { q.Status = operations.QuestionAnswered }},
		{"blank text", func(q *operations.Question) { q.Text = " \t\n" }},
		{"blank option", func(q *operations.Question) { q.Options = []string{"yes", " "} }},
		{"too many options", func(q *operations.Question) { q.Options = []string{"1", "2", "3", "4", "5"} }},
		{"missing asked time", func(q *operations.Question) { q.AskedAt = time.Time{} }},
		{"missing deadline", func(q *operations.Question) { q.ExpiresAt = time.Time{} }},
		{"invalid deadline", func(q *operations.Question) { q.ExpiresAt = q.AskedAt }},
		{"answer", func(q *operations.Question) { q.Answer = "yes" }},
		{"answer time", func(q *operations.Question) { q.AnsweredAt = &q.AskedAt }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			question := newStoreQuestion("")
			tc.change(&question)
			assertQuestionRejected(t, dir, op.ID, func() (operations.Operation, error) {
				return store.AskQuestion(t.Context(), op.ID, question, 0)
			}, nil)
		})
	}
}

func TestOperationQuestionUsesLatestStoredEligibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer bool
		change func(*operations.Operation)
	}{
		{"ask stale version", false, func(op *operations.Operation) { op.Tasks[0].Continuation.Version++ }},
		{"ask missing task", false, func(op *operations.Operation) { op.Tasks = nil }},
		{"ask missing continuation", false, func(op *operations.Operation) { op.Tasks[0].Continuation = nil }},
		{"ask wrong owner", false, func(op *operations.Operation) { op.Tasks[0].Continuation.DecisionBy = operations.DecisionCode }},
		{"ask resolved", false, func(op *operations.Operation) { op.Tasks[0].Continuation.Status = operations.ContinuationResolved }},
		{"ask terminal", false, func(op *operations.Operation) { op.Status = operations.Cancelled }},
		{"answer missing task", true, func(op *operations.Operation) { op.Tasks = nil }},
		{"answer wrong link", true, func(op *operations.Operation) { op.Tasks[0].Continuation.QuestionID = id.QuestionID() }},
		{"answer wrong owner", true, func(op *operations.Operation) { op.Tasks[0].Continuation.DecisionBy = operations.DecisionModel }},
		{"answer resolved", true, func(op *operations.Operation) { op.Tasks[0].Continuation.Status = operations.ContinuationResolved }},
		{"answer expired", true, func(op *operations.Operation) { op.Questions[0].Status = operations.QuestionExpired }},
		{"answer terminal", true, func(op *operations.Operation) { op.Status = operations.Completed }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			op, _ := createContinuationOperation(t, first)
			question := newStoreQuestion(op.Tasks[0].ID)
			if tc.answer {
				if _, err := first.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				tc.change(next)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			assertQuestionRejected(t, dir, op.ID, func() (operations.Operation, error) {
				if tc.answer {
					return first.AnswerQuestion(t.Context(), op.ID, question.ID, "yes")
				}
				return first.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version)
			}, nil)
		})
	}
}

func TestOperationQuestionLateAnswerCommitsExpiration(t *testing.T) {
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op, _ := createContinuationOperation(t, store)
	question := newStoreQuestion(op.Tasks[0].ID)
	question.AskedAt, question.ExpiresAt = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
	asked, err := store.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	expired, err := store.AnswerQuestion(t.Context(), op.ID, question.ID, "too late")
	if err != nil {
		t.Fatalf("late answer must return its committed expiration snapshot: %v", err)
	}
	q := expired.Questions[0]
	if q.Status != operations.QuestionExpired || q.Answer != "" || q.AnsweredAt != nil || expired.Status != operations.Planning || countResultEvents(expired, operations.QuestionAnsweredEvent) != 0 {
		t.Fatalf("late answer was accepted or expiration was lost: %+v", expired)
	}
	assertQuestionContinuation(t, expired, operations.DecisionModel, operations.ContinuationPending, question.ID, asked.Tasks[0].Continuation.Version+1)
	assertQuestionEvent(t, expired, operations.QuestionExpiredEvent, question.ID, len(asked.Events)+1, started)
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, expired)
	assertQuestionRejected(t, dir, op.ID, func() (operations.Operation, error) {
		return store.AnswerQuestion(t.Context(), op.ID, question.ID, "still too late")
	}, nil)
}

func TestOperationQuestionAnswerAndSweepHaveOneWinner(t *testing.T) {
	dir := t.TempDir()
	first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op, _ := createContinuationOperation(t, first)
	question := newStoreQuestion(op.Tasks[0].ID)
	asked, err := first.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version)
	if err != nil {
		t.Fatal(err)
	}
	gate, answers, sweeps := make(chan struct{}), make(chan error, 1), make(chan error, 1)
	go func() {
		<-gate
		_, err := first.AnswerQuestion(t.Context(), op.ID, question.ID, "yes")
		answers <- err
	}()
	go func() {
		<-gate
		_, err := second.ExpireQuestions(t.Context(), op.ID, question.ExpiresAt)
		sweeps <- err
	}()
	close(gate)
	answerErr, sweepErr := <-answers, <-sweeps
	if sweepErr != nil {
		t.Fatal(sweepErr)
	}
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := saved.Questions[0]
	if q.Status == operations.QuestionAnswered {
		if answerErr != nil || q.Answer != "yes" || q.AnsweredAt == nil {
			t.Fatalf("winning answer was lost: %+v, %v", q, answerErr)
		}
	} else if q.Status != operations.QuestionExpired || answerErr == nil || q.Answer != "" || q.AnsweredAt != nil {
		t.Fatalf("winning expiration was lost: %+v, %v", q, answerErr)
	}
	if len(saved.Events) != len(asked.Events)+1 || countResultEvents(saved, operations.QuestionAnsweredEvent)+countResultEvents(saved, operations.QuestionExpiredEvent) != 1 {
		t.Fatalf("answer and sweep published duplicate events: %+v", saved.Events)
	}
	assertQuestionContinuation(t, saved, operations.DecisionModel, operations.ContinuationPending, question.ID, asked.Tasks[0].Continuation.Version+1)
	path := filepath.Join(dir, "operations", op.ID+".json")
	before := readDispatchFile(t, path)
	if _, err := first.ExpireQuestions(t.Context(), op.ID, question.ExpiresAt); err != nil || !bytes.Equal(before, readDispatchFile(t, path)) {
		t.Fatalf("repeated sweep rewrote settled state: %v", err)
	}
}

func TestOperationQuestionExpirationPreservesTerminalState(t *testing.T) {
	for _, status := range []operations.Status{operations.Completed, operations.Failed, operations.Cancelled} {
		t.Run(string(status), func(t *testing.T) {
			dir := t.TempDir()
			store := newDispatchStore(t, dir)
			op, _ := createContinuationOperation(t, store)
			question := newStoreQuestion(op.Tasks[0].ID)
			if _, err := store.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version); err != nil {
				t.Fatal(err)
			}
			terminal, err := store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				next.Status = status
				next.Tasks[0].Continuation.Status = operations.ContinuationResolved
				next.Tasks[0].Continuation.Version++
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			expired, err := newDispatchStore(t, dir).ExpireQuestions(t.Context(), op.ID, question.ExpiresAt)
			if err != nil || expired.Status != status || expired.Questions[0].Status != operations.QuestionExpired || expired.Questions[0].Answer != "" || expired.Questions[0].AnsweredAt != nil || countResultEvents(expired, operations.QuestionExpiredEvent) != 1 {
				t.Fatalf("terminal expiration revived the operation or supplied an answer: %+v, %v", expired, err)
			}
			assertResultJSONEqual(t, expired.Tasks, terminal.Tasks)
		})
	}
}

func newStoreQuestion(taskID string) operations.Question {
	now := time.Now().UTC()
	return operations.Question{ID: id.QuestionID(), TaskID: taskID, Status: operations.QuestionPending,
		Text: "Continue review?", Options: []string{"yes", "no"}, AskedAt: now, ExpiresAt: now.Add(time.Hour)}
}

func assertQuestionRejected(t *testing.T, dir, operationID string, call func() (operations.Operation, error), want error) {
	t.Helper()
	path := filepath.Join(dir, "operations", operationID+".json")
	before := readDispatchFile(t, path)
	published, err := call()
	if err == nil || want != nil && !errors.Is(err, want) || published.ID != "" {
		t.Fatalf("question transition must reject without publishing a snapshot: %+v, %v", published, err)
	}
	if !bytes.Equal(before, readDispatchFile(t, path)) {
		t.Fatal("rejected question transition changed operation bytes or storage revision")
	}
}

func assertQuestionContinuation(t *testing.T, op operations.Operation, decision operations.DecisionBy, status operations.ContinuationStatus, questionID string, version uint64) {
	t.Helper()
	c := op.Tasks[0].Continuation
	if c == nil || c.DecisionBy != decision || c.Status != status || c.QuestionID != questionID || c.Version != version {
		t.Fatalf("question and linked decision were not committed together: %+v", c)
	}
}

func assertQuestionEvent(t *testing.T, op operations.Operation, kind operations.EventType, questionID string, count int, started time.Time) {
	t.Helper()
	if len(op.Events) != count || op.UpdatedAt.Before(started) || op.UpdatedAt.After(time.Now().UTC()) {
		t.Fatalf("question did not commit one event and timestamp: %+v", op)
	}
	event := op.Events[len(op.Events)-1]
	if event.Type != kind || event.OperationID != op.ID || event.QuestionID != questionID || event.Status != op.Status || event.Sequence != uint64(count) || event.CreatedAt.Before(started) || event.CreatedAt.After(op.UpdatedAt) {
		t.Fatalf("question event lost its identity or commit time: %+v", event)
	}
}
