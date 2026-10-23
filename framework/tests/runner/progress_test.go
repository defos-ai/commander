package runner_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestOperationProgressRequiresKnownFinishedOutcomes(t *testing.T) {
	now := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		status   operations.TaskStatus
		finished bool
	}{
		{operations.TaskQueued, false},
		{operations.TaskRunning, false},
		{operations.TaskInterrupted, false},
		{"unknown", false},
		{operations.TaskSucceeded, true},
		{operations.TaskFailed, true},
		{operations.TaskCancelled, true},
	} {
		t.Run(string(test.status), func(t *testing.T) {
			// Interrupted executions can retain a timestamp without a known outcome.
			op := operations.Operation{Status: operations.Cancelled, Tasks: []operations.Task{{Status: test.status, FinishedAt: &now}}}
			progress := op.AssessProgress(now)
			finished := 0
			if test.finished {
				finished = 1
			}
			if progress.TotalTasks != 1 || progress.FinishedTasks != finished || progress.Settled != test.finished ||
				!reflect.DeepEqual(progress.TaskCounts, map[operations.TaskStatus]int{test.status: 1}) {
				t.Fatalf("incorrect progress for saved outcome: %+v", progress)
			}
			op.Status = operations.Planning
			if op.AssessProgress(now).Settled {
				t.Fatal("an active operation cannot be settled, even when all current tasks are finished")
			}
		})
	}
	for _, status := range []operations.Status{operations.Completed, operations.Failed, operations.Cancelled} {
		progress := (operations.Operation{Status: status}).AssessProgress(now)
		if !progress.Settled || progress.TotalTasks != 0 || progress.FinishedTasks != 0 || progress.TaskCounts == nil || len(progress.TaskCounts) != 0 {
			t.Fatalf("empty terminal operation has incorrect progress: %+v", progress)
		}
	}
}

func TestOperationProgressSettlesLateOutcomeWithoutReopeningDecisions(t *testing.T) {
	now := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	op := operations.Operation{ID: id.OperationID(), Status: operations.Running,
		Questions: []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionPending,
			Text: "Continue?", AskedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute)}},
		Tasks: []operations.Task{
			{Status: operations.TaskSucceeded, Continuation: &operations.Continuation{Status: operations.ContinuationPending, DecisionBy: operations.DecisionModel}},
			{ID: id.TaskID(), AgentID: "fixture-agent-12345678", Status: operations.TaskInterrupted, FinishedAt: &now},
		},
	}
	op.Tasks[1].OperationID, op.Tasks[1].CommandID = op.ID, op.Tasks[1].ID
	before := op.AssessProgress(now)
	if before.FinishedTasks != 1 || before.TotalTasks != 2 || before.PendingDecisions != 1 || before.PendingQuestions != 1 || before.Settled {
		t.Fatalf("unexpected progress before cancellation: %+v", before)
	}
	if err := op.Cancel(); err != nil {
		t.Fatal(err)
	}
	cancelled := op.AssessProgress(now)
	if cancelled.Settled || cancelled.FinishedTasks != 1 || cancelled.PendingDecisions != 0 || cancelled.PendingQuestions != 0 || cancelled.LastQuestion != nil || cancelled.RemainingQuestions != 0 {
		t.Fatalf("cancellation presented unfinished work as settled or retained actionable decisions: %+v", cancelled)
	}
	changed, err := op.FinishTask(operations.TaskResult{TaskID: op.Tasks[1].ID, AgentID: op.Tasks[1].AgentID,
		Status: operations.TaskFailed, Error: "execution failed", FinishedAt: now.Add(time.Second)})
	if err != nil || !changed {
		t.Fatalf("late outcome was not accepted: changed=%v, err=%v", changed, err)
	}
	saved := op.Clone()
	settled := op.AssessProgress(now.Add(time.Second))
	if !settled.Settled || settled.FinishedTasks != 2 || settled.TotalTasks != 2 || settled.PendingDecisions != 0 || settled.PendingQuestions != 0 || settled.LastQuestion != nil {
		t.Fatalf("late outcome did not settle terminal progress: %+v", settled)
	}
	if op.Status != operations.Cancelled || op.Questions[0].Status != operations.QuestionPending ||
		op.Tasks[0].Continuation.Status != operations.ContinuationPending || op.Tasks[1].Continuation.Status != operations.ContinuationPending ||
		!reflect.DeepEqual(saved, op.Clone()) {
		t.Fatal("progress changed terminal status, question history or retained model decisions")
	}
}

func TestOperationProgressSelectsQuestionsWithoutMutatingHistory(t *testing.T) {
	now := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	asked := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	op := operations.Operation{Status: operations.Waiting,
		Questions: []operations.Question{
			{ID: "older", Status: operations.QuestionPending, AskedAt: asked.Add(-time.Minute), ExpiresAt: future},
			{ID: "b", Status: operations.QuestionPending, AskedAt: asked, ExpiresAt: future, Options: []string{"yes", "no"}},
			{ID: "a", Status: operations.QuestionPending, AskedAt: asked, ExpiresAt: future},
			{ID: "deadline", Status: operations.QuestionPending, AskedAt: now, ExpiresAt: now},
			{ID: "answered", Status: operations.QuestionAnswered, AskedAt: now, ExpiresAt: future},
			{ID: "expired", Status: operations.QuestionExpired, AskedAt: now, ExpiresAt: future},
			{ID: "no-deadline", Status: operations.QuestionPending, AskedAt: asked.Add(-2 * time.Minute)},
		},
	}
	for _, continuation := range []*operations.Continuation{
		{DecisionBy: operations.DecisionModel, Status: operations.ContinuationPending},
		{DecisionBy: operations.DecisionModel, Status: operations.ContinuationWaiting},
		{DecisionBy: operations.DecisionModel, Status: operations.ContinuationResolved},
		{DecisionBy: operations.DecisionCode, Status: operations.ContinuationPending},
		{DecisionBy: operations.DecisionCode, Status: operations.ContinuationWaiting},
		{DecisionBy: operations.DecisionUser, Status: operations.ContinuationPending},
		{DecisionBy: operations.DecisionUser, Status: operations.ContinuationWaiting},
		nil,
	} {
		op.Tasks = append(op.Tasks, operations.Task{Status: operations.TaskSucceeded, Continuation: continuation})
	}
	saved := op.Clone()
	progress := op.AssessProgress(now)
	if progress.PendingDecisions != 1 || progress.PendingQuestions != 4 || progress.RemainingQuestions != 3 || progress.LastQuestion == nil || progress.LastQuestion.ID != "b" {
		t.Fatalf("question selection or model decision count is incorrect: %+v", progress)
	}
	if progress.TotalTasks != 8 || progress.FinishedTasks != 8 || progress.TaskCounts[operations.TaskSucceeded] != 8 || progress.Settled {
		t.Fatalf("finished tasks hid an active operation's decisions: %+v", progress)
	}
	progress.TaskCounts[operations.TaskSucceeded] = 100
	progress.LastQuestion.ID = "changed"
	progress.LastQuestion.Options[0] = "changed"
	again := op.AssessProgress(now)
	if again.TaskCounts[operations.TaskSucceeded] != 8 || again.LastQuestion.ID != "b" || again.LastQuestion.Options[0] != "yes" || !reflect.DeepEqual(saved, op.Clone()) {
		t.Fatal("progress snapshots alias each other or mutate saved question history")
	}
}
