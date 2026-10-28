package playground_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/chat"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
)

func TestStopWithoutActiveReply(t *testing.T) {
	for _, settled := range []bool{false, true} {
		t.Run(fmt.Sprintf("settled operation watched=%t", settled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			commander := fakes.NewCommander(bridge.Operation{ID: "done", Status: "completed", Progress: bridge.Progress{Settled: true}})
			model := fakes.NewReviewModel(false)
			engine := chat.New(ctx, model, commander)
			defer engine.Close()
			if settled {
				sendStopCommand(t, engine, "/watch done")
				waitStopEvent(t, ctx, engine, "operation")
				waitStopEvent(t, ctx, engine, "idle")
				select {
				case <-model.Started():
				case <-ctx.Done():
					t.Fatal("settled operation was not reviewed")
				}
			}
			for range 2 {
				sendStopCommand(t, engine, "/stop")
				if notice := waitStopEvent(t, ctx, engine, "notice"); notice.Text != "No reply is running." {
					t.Fatalf("idle stop notice: %q", notice.Text)
				}
			}
			select {
			case <-model.Started():
				t.Fatal("idle stop invoked the model")
			default:
			}
		})
	}
}

func TestStopCancelsReplyAndClearsQueuedMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	model := fakes.NewReviewModel(true)
	engine := chat.New(ctx, model, fakes.NewCommander(bridge.Operation{}))
	defer engine.Close()
	sendStopCommand(t, engine, "start")
	select {
	case <-model.Started():
	case <-ctx.Done():
		t.Fatal("model did not start")
	}
	sendStopCommand(t, engine, "queued message")
	if notice := waitStopEvent(t, ctx, engine, "notice"); !strings.Contains(notice.Text, "Message queued") {
		t.Fatalf("queued message notice: %q", notice.Text)
	}
	sendStopCommand(t, engine, "/stop")
	if notice := waitStopEvent(t, ctx, engine, "notice"); notice.Text != "Reply stopped." {
		t.Fatalf("active reply stop notice: %q", notice.Text)
	}
	waitStopEvent(t, ctx, engine, "idle")
	sendStopCommand(t, engine, "/status")
	if notice := waitStopEvent(t, ctx, engine, "notice"); !strings.Contains(notice.Text, "0 queued messages") {
		t.Fatalf("stop retained queued input: %q", notice.Text)
	}
	select {
	case <-model.Started():
		t.Fatal("stopped reply or cleared message was retried")
	default:
	}
}

func TestStopPausesReviewWhileLateOutcomeRemainsVisible(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	op := bridge.Operation{ID: "late", Status: "completed", UpdatedAt: time.Now(), Progress: bridge.Progress{
		TaskCounts: map[string]int{"interrupted": 1}, TotalTasks: 1,
	}}
	commander := fakes.NewCommander(op)
	model := fakes.NewReviewModel(false)
	engine := chat.New(ctx, model, commander)
	defer engine.Close()
	sendStopCommand(t, engine, "/watch late")
	waitStopEvent(t, ctx, engine, "operation")
	for range 2 {
		sendStopCommand(t, engine, "/stop")
		notice := waitStopEvent(t, ctx, engine, "notice")
		if notice.Text != "Model review paused; /watch <id> resumes it. Operation updates remain visible." {
			t.Fatalf("terminal operation stop notice: %q", notice.Text)
		}
	}
	op.UpdatedAt = op.UpdatedAt.Add(time.Second)
	op.Progress = bridge.Progress{TaskCounts: map[string]int{"succeeded": 1}, TotalTasks: 1, FinishedTasks: 1, Settled: true}
	commander.Publish(op)
	if update := waitStopEvent(t, ctx, engine, "operation"); !update.Operation.Settled() {
		t.Fatal("stop hid the late task outcome")
	}
	// The command response is a barrier after the owner processed the snapshot.
	sendStopCommand(t, engine, "/status")
	waitStopEvent(t, ctx, engine, "notice")
	select {
	case <-model.Started():
		t.Fatal("late outcome resumed model review without operator input")
	default:
	}
	sendStopCommand(t, engine, "/watch late")
	waitStopEvent(t, ctx, engine, "idle")
	select {
	case <-model.Started():
	default:
		t.Fatal("watch did not resume final model review")
	}
	sendStopCommand(t, engine, "/stop")
	if notice := waitStopEvent(t, ctx, engine, "notice"); notice.Text != "No reply is running." {
		t.Fatalf("settled operation stop notice: %q", notice.Text)
	}
}

func sendStopCommand(t *testing.T, engine *chat.Engine, command string) {
	t.Helper()
	if err := engine.Send(command); err != nil {
		t.Fatal(err)
	}
}

func waitStopEvent(t *testing.T, ctx context.Context, engine *chat.Engine, kind string) chat.Event {
	t.Helper()
	for {
		select {
		case event, open := <-engine.Events():
			if !open {
				t.Fatal("chat engine closed before " + kind)
			}
			if event.Kind == "error" {
				t.Fatal(event.Text)
			}
			if event.Kind == kind {
				return event
			}
		case <-ctx.Done():
			t.Fatal("chat engine did not emit " + kind)
		}
	}
}
