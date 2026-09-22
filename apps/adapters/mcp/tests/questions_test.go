package cli_test

import (
	"testing"
	"time"

	"commander/framework/domain/operations"
)

type questionOperation struct {
	operations.Operation
	LastQuestion       *operations.Question `json:"last_question"`
	RemainingQuestions int                  `json:"remaining_questions"`
}

func TestMCPQuestionSelectionAndExpiry(t *testing.T) {
	// Arrange: real MCP and controller hosts, with multiple operator questions.
	d := newDeployment(t)
	seedMission(t, d)
	endpoint, _ := d.mcp(t, false)
	session := connect(t, d.ctx, endpoint)
	op := startMission(t, d, session)
	read := func() questionOperation {
		return decode[questionOperation](t, call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": op.ID}))
	}
	ask := func(text string, timeout int) operations.Question {
		args := map[string]any{"operation_id": op.ID, "text": text, "options": []string{"Continue", "Use fallback", "Finish", "Cancel"}}
		if timeout != 0 {
			args["timeout_seconds"] = timeout
		}
		return decode[operations.Question](t, call(t, d.ctx, session, "ask_question", args))
	}
	first := ask("First decision", 0)
	second := ask("Second decision", 0)
	latest := ask("Short-lived decision", 3)
	for _, question := range []operations.Question{first, second, latest} {
		if question.Status != operations.QuestionPending {
			t.Fatalf("new question is not pending: %+v", question)
		}
	}

	// Act: query the latest pending question and its remaining queue.
	current := read()
	if current.LastQuestion == nil || current.LastQuestion.ID != latest.ID || current.RemainingQuestions != 2 || len(current.Questions) != 3 {
		t.Fatalf("wrong active question: %+v", current)
	}
	if first.ExpiresAt.Sub(first.AskedAt) != 2*time.Minute || latest.ExpiresAt.Sub(latest.AskedAt) != 3*time.Second {
		t.Fatalf("question deadlines: first=%+v latest=%+v", first, latest)
	}
	listed := decode[struct {
		Operations []struct {
			LastQuestion       *operations.Question `json:"last_question"`
			RemainingQuestions int                  `json:"remaining_questions"`
			PendingQuestions   int                  `json:"pending_questions"`
		}
	}](t, call(t, d.ctx, session, "list_operations", map[string]any{}))
	if len(listed.Operations) != 1 || listed.Operations[0].LastQuestion == nil || listed.Operations[0].LastQuestion.ID != latest.ID || listed.Operations[0].RemainingQuestions != 2 || listed.Operations[0].PendingQuestions != 3 {
		t.Fatalf("listing lost active question: %+v", listed)
	}

	// Assert: expiry removes the question from the active view without inventing
	// an operator answer or losing the original question from durable history.
	poll(t, func() bool {
		current = read()
		return current.Questions[2].Status == operations.QuestionExpired
	})
	if current.LastQuestion == nil || current.LastQuestion.ID != second.ID || current.LastQuestion.Status != operations.QuestionPending || current.RemainingQuestions != 1 || current.Questions[2].Answer != "" || current.Questions[2].AnsweredAt != nil || !current.Questions[2].ExpiresAt.Equal(latest.ExpiresAt) {
		t.Fatalf("expiry lost history or selected the wrong question: %+v", current)
	}
	rejected(t, d.ctx, session, "answer_question", map[string]any{"operation_id": op.ID, "question_id": latest.ID, "answer": "Continue"})
	for _, question := range []operations.Question{second, first} {
		call(t, d.ctx, session, "answer_question", map[string]any{"operation_id": op.ID, "question_id": question.ID, "answer": "Finish"})
		current = read()
		for _, saved := range current.Questions {
			if saved.ID == question.ID && (saved.Status != operations.QuestionAnswered || saved.AnsweredAt == nil || saved.Answer != "Finish") {
				t.Fatalf("answer status was not saved: %+v", saved)
			}
		}
		if question.ID == second.ID && (current.LastQuestion == nil || current.LastQuestion.ID != first.ID || current.RemainingQuestions != 0) {
			t.Fatalf("answer did not reveal the next question: %+v", current)
		}
	}
	if current.LastQuestion != nil || current.RemainingQuestions != 0 || current.Status != operations.Planning || len(current.Questions) != 3 {
		t.Fatalf("resolved queue still blocks planning: %+v", current)
	}
	for _, extra := range []map[string]any{
		{"options": []string{"A", "B", "C", "D", "E"}},
		{"timeout_seconds": -1},
		{"timeout_seconds": 86401},
	} {
		extra["operation_id"], extra["text"] = op.ID, "Invalid question"
		rejected(t, d.ctx, session, "ask_question", extra)
	}
}
