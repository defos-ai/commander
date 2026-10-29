package playground_test

import (
	"context"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/chat"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestQuestionFallbackWakesWithoutSavedUpdateAndAnswersOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	op := fixtures.QuestionFallbackOperation(time.Now().Add(time.Second))
	commander := fakes.NewQuestionFallbackCommander(op)
	model := fakes.NewQuestionFallbackModel(op.ID, fixtures.QuestionID, false)
	engine := chat.New(ctx, model, commander)
	defer engine.Close()
	sendStopCommand(t, engine, "/watch "+op.ID)
	waitStopEvent(t, ctx, engine, "operation")

	sendStopCommand(t, engine, "Review current work")
	before := waitFallbackReview(t, ctx, model)
	waitStopEvent(t, ctx, engine, "idle")
	if before.AnswerAvailable {
		t.Fatal("answer tool was available during the operator window")
	}
	ready := waitFallbackOperation(t, ctx, engine, func(next bridge.Operation) bool {
		return next.Questions[0].Pending() && !time.Now().Before(op.Questions[0].FallbackAt)
	})
	if !ready.UpdatedAt.Equal(op.UpdatedAt) || ready.Progress.PendingQuestions != op.Progress.PendingQuestions {
		t.Fatal("time-based fallback required a saved operation or progress change")
	}
	waitFallbackOperation(t, ctx, engine, func(next bridge.Operation) bool { return next.Questions[0].Status == bridge.QuestionAnswered })
	reads := commander.ReadCount() + 2
	waitFallbackReads(t, ctx, commander, reads)

	saved, _ := commander.Operation(ctx, op.ID)
	if saved.Questions[0].AnsweredBy != "model" || commander.AnswerCount() != 1 || model.EligibleReviews() != 1 {
		t.Fatal("fallback did not save one model answer and deduplicate repeated snapshots")
	}
}

func TestQuestionFallbackRechecksOperatorAnswerBeforeModelMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	op := fixtures.QuestionFallbackOperation(time.Now().Add(-time.Second))
	commander := fakes.NewQuestionFallbackCommander(op)
	model := fakes.NewQuestionFallbackModel(op.ID, fixtures.QuestionID, true)
	engine := chat.New(ctx, model, commander)
	defer engine.Close()
	sendStopCommand(t, engine, "/watch "+op.ID)
	if !waitFallbackReview(t, ctx, model).AnswerAvailable {
		t.Fatal("eligible fallback review did not advertise the answer tool")
	}

	accepted := commander.OperatorAnswer("Use the operator's choice")
	if err := engine.ObserveOperation(accepted); err != nil {
		t.Fatal(err)
	}
	model.Release()
	waitStaleFallbackReply(t, ctx, engine)
	waitFallbackReads(t, ctx, commander, commander.ReadCount()+2)

	saved, _ := commander.Operation(ctx, op.ID)
	if commander.AnswerCount() != 0 || saved.Questions[0].AnsweredBy != "operator" || saved.Questions[0].Answer != "Use the operator's choice" {
		t.Fatal("stale model output submitted an answer after the operator had already answered")
	}
}

func TestStopDefersQuestionFallbackUntilWatchResumes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	op := fixtures.QuestionFallbackOperation(time.Now().Add(time.Second))
	commander := fakes.NewQuestionFallbackCommander(op)
	model := fakes.NewQuestionFallbackModel(op.ID, fixtures.QuestionID, false)
	engine := chat.New(ctx, model, commander)
	defer engine.Close()
	sendStopCommand(t, engine, "/watch "+op.ID)
	waitStopEvent(t, ctx, engine, "operation")
	sendStopCommand(t, engine, "/stop")
	waitStopEvent(t, ctx, engine, "notice")

	waitFallbackOperation(t, ctx, engine, func(next bridge.Operation) bool {
		return next.Questions[0].Pending() && !time.Now().Before(op.Questions[0].FallbackAt)
	})
	sendStopCommand(t, engine, "/status")
	waitStopEvent(t, ctx, engine, "notice")
	if model.EligibleReviews() != 0 || commander.AnswerCount() != 0 {
		t.Fatal("paused model review answered when the operator window ended")
	}
	sendStopCommand(t, engine, "/watch "+op.ID)
	waitFallbackOperation(t, ctx, engine, func(next bridge.Operation) bool { return next.Questions[0].Status == bridge.QuestionAnswered })

	if commander.AnswerCount() != 1 {
		t.Fatal("watch did not resume the pending fallback")
	}
}

func waitStaleFallbackReply(t *testing.T, ctx context.Context, engine *chat.Engine) {
	t.Helper()
	for {
		select {
		case event, open := <-engine.Events():
			if !open {
				t.Fatal("engine closed before the stale fallback reply finished")
			}
			if event.Kind == "error" && event.CallID != "fallback-answer" {
				t.Fatal(event.Text)
			}
			if event.Kind == "idle" {
				return
			}
		case <-ctx.Done():
			t.Fatal("stale fallback reply did not finish")
		}
	}
}

func waitFallbackReview(t *testing.T, ctx context.Context, model *fakes.QuestionFallbackModel) fakes.FallbackReview {
	t.Helper()
	select {
	case review := <-model.Reviews():
		return review
	case <-ctx.Done():
		t.Fatal("model did not review the fallback question")
		return fakes.FallbackReview{}
	}
}

func waitFallbackOperation(t *testing.T, ctx context.Context, engine *chat.Engine, check func(bridge.Operation) bool) bridge.Operation {
	t.Helper()
	for {
		select {
		case event, open := <-engine.Events():
			if !open {
				t.Fatal("engine closed while awaiting fallback")
			}
			if event.Kind == "error" {
				t.Fatal(event.Text)
			}
			if event.Operation != nil && check(*event.Operation) {
				return *event.Operation
			}
		case <-ctx.Done():
			t.Fatal("question fallback did not update its watched operation")
			return bridge.Operation{}
		}
	}
}

func waitFallbackReads(t *testing.T, ctx context.Context, commander *fakes.QuestionFallbackCommander, target int32) {
	t.Helper()
	for commander.ReadCount() < target {
		select {
		case <-commander.Reads():
		case <-ctx.Done():
			t.Fatal("question watcher stopped refreshing")
		}
	}
}
