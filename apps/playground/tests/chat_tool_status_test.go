package playground_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/chat"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
	"github.com/defos-ai/commander/framework/id"
)

func TestToolCallEventsPreserveModelIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	calls := fixtures.ToolStatusCalls()
	commander := fakes.NewToolStatusCommander(fixtures.ToolStatusName, fixtures.ToolStatusReplies())
	commander.ReleaseAll()
	engine := chat.New(ctx, fakes.NewToolStatusModel(calls, "Before", "After"), commander)
	defer engine.Close()

	if err := engine.Send("Inspect twice"); err != nil {
		t.Fatal(err)
	}
	events := toolStatusEventsUntil(t, ctx, engine, "idle", 1)

	var statuses []chat.Event
	for _, event := range events {
		if event.CallID != "" {
			statuses = append(statuses, event)
			if event.ID != "" || event.OperationID != "" {
				t.Fatalf("tool identity leaked into operation identity: %+v", event)
			}
		}
	}
	if len(statuses) != 4 {
		t.Fatalf("tool statuses: %+v", statuses)
	}
	for i, call := range calls {
		pending, terminal := statuses[2*i], statuses[2*i+1]
		if pending.Kind != "tool" || pending.CallID != call.ID || terminal.CallID != call.ID {
			t.Fatalf("tool call identity changed: pending=%+v terminal=%+v", pending, terminal)
		}
	}
	if statuses[1].Kind != "success" || statuses[1].Text != fixtures.ToolStatusName+" completed" ||
		statuses[3].Kind != "error" || !strings.Contains(statuses[3].Text, "fixture tool detail") {
		t.Fatalf("tool outcomes or error detail changed: %+v", statuses)
	}
}

func TestToolCallTransportFailureRetainsStatusIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	calls := fixtures.ToolStatusCalls()[:1]
	replies := fixtures.ToolStatusReplies()
	replies["first"] = fakes.ToolStatusReply{Error: errors.New("fixture transport failed")}
	commander := fakes.NewToolStatusCommander(fixtures.ToolStatusName, replies)
	commander.ReleaseAll()
	engine := chat.New(ctx, fakes.NewToolStatusModel(calls, "Before", "After"), commander)
	defer engine.Close()

	if err := engine.Send("Inspect once"); err != nil {
		t.Fatal(err)
	}
	events := toolStatusEventsUntil(t, ctx, engine, "idle", 1)

	var failure chat.Event
	for _, event := range events {
		if event.Kind == "error" {
			failure = event
		}
	}
	if failure.CallID != calls[0].ID || !strings.Contains(failure.Text, "fixture transport failed") ||
		!strings.Contains(failure.Text, "inspect state before retrying") {
		t.Fatalf("transport failure lost identity or recovery detail: %+v", failure)
	}
}

func TestConcurrentOperatorToolCallsHaveDistinctStatusIdentities(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	commander := fakes.NewToolStatusCommander(fixtures.ToolStatusName, map[string]fakes.ToolStatusReply{
		"": {Result: bridge.Result{Data: []byte(`{"operations":[]}`)}},
	})
	engine := chat.New(ctx, fakes.NewReviewModel(false), commander)
	defer engine.Close()

	for range 2 {
		if err := engine.Send("/operations"); err != nil {
			t.Fatal(err)
		}
	}
	pending := toolStatusEventsUntil(t, ctx, engine, "tool", 2)
	commander.Release("")
	completed := toolStatusEventsUntil(t, ctx, engine, "control_end", 2)

	identities := map[string]bool{}
	for _, event := range pending {
		if event.Kind == "tool" {
			_, exists := identities[event.CallID]
			if !id.Valid(event.CallID) || exists {
				t.Fatalf("operator calls share or lack an identity: %+v", pending)
			}
			identities[event.CallID] = false
		}
	}
	for _, event := range completed {
		if event.Kind == "success" {
			if _, exists := identities[event.CallID]; !exists {
				t.Fatalf("terminal status has no matching call: %+v", event)
			}
			identities[event.CallID] = true
		}
	}
	if len(identities) != 2 {
		t.Fatalf("operator tool identities: %+v", identities)
	}
	for identity, completed := range identities {
		if !completed {
			t.Fatalf("operator call %s never completed", identity)
		}
	}
}

func TestUnavailableToolStatusPreservesModelIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	calls := fixtures.ToolStatusCalls()[:1]
	calls[0].Name = "unavailable_tool"
	commander := fakes.NewToolStatusCommander(fixtures.ToolStatusName, fixtures.ToolStatusReplies())
	engine := chat.New(ctx, fakes.NewToolStatusModel(calls, "Before", "After"), commander)
	defer engine.Close()

	if err := engine.Send("Try an unavailable tool"); err != nil {
		t.Fatal(err)
	}
	events := toolStatusEventsUntil(t, ctx, engine, "idle", 1)

	var failure chat.Event
	for _, event := range events {
		if event.Kind == "tool" {
			t.Fatal("an unavailable tool was dispatched")
		}
		if event.Kind == "error" {
			failure = event
		}
	}
	if failure.CallID != calls[0].ID || !strings.Contains(failure.Text, "This tool is not available") {
		t.Fatalf("unavailable tool lost its terminal identity: %+v", failure)
	}
}

func toolStatusEventsUntil(t *testing.T, ctx context.Context, engine *chat.Engine, kind string, count int) []chat.Event {
	t.Helper()
	var events []chat.Event
	for count > 0 {
		select {
		case event, open := <-engine.Events():
			if !open {
				t.Fatal("chat closed before " + kind)
			}
			events = append(events, event)
			if event.Kind == kind {
				count--
			}
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v", kind, ctx.Err())
		}
	}
	return events
}
