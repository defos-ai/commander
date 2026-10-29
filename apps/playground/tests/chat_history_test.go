package playground_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/chat"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestChatRestoresContextWithoutReplayingTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	previous := []llm.Message{
		{Role: "user", Text: "Create an operation"},
		{Role: "assistant", Calls: []llm.Call{{ID: "saved-call", Name: "start_operation", Arguments: json.RawMessage(`{"operation_id":"saved-operation"}`)}}},
		{Role: "tool", CallID: "saved-call", Name: "start_operation", Text: "Operation created"},
		{Role: "assistant", Text: "Operation is running"},
	}
	encoded, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &previous); err != nil {
		t.Fatal(err)
	}
	model := fakes.NewHistoryModel()
	engine := chat.NewWithHistory(ctx, model, fakes.NewCommander(bridge.Operation{}), previous)
	defer engine.Close()
	previous[0].Text = "changed after construction"
	previous[1].Calls[0].Name = "changed after construction"

	if err := engine.Send("/status"); err != nil {
		t.Fatal(err)
	}
	waitStopEvent(t, ctx, engine, "notice")
	select {
	case <-model.Requests():
		t.Fatal("restoring context invoked the model")
	default:
	}
	if err := engine.Send("What happened earlier?"); err != nil {
		t.Fatal(err)
	}

	var restored []llm.Message
	select {
	case restored = <-model.Requests():
	case <-ctx.Done():
		t.Fatal("model did not receive restored context")
	}
	if len(restored) != 5 || restored[0].Text != "Create an operation" || restored[1].Calls[0].Name != "start_operation" || restored[4].Text != "What happened earlier?" || len(restored[0].Native) != 0 {
		t.Fatalf("restored context changed: %+v", restored)
	}
	var checkpoint []llm.Message
	for _, event := range toolStatusEventsUntil(t, ctx, engine, "idle", 1) {
		if event.Kind == "tool" {
			t.Fatal("a saved tool call was executed")
		}
		if event.Kind == "history" {
			checkpoint = event.History
		}
	}
	if len(checkpoint) != 6 || checkpoint[5].Role != "assistant" {
		t.Fatalf("completed turn was not checkpointed: %+v", checkpoint)
	}
}

func TestChatRestoresInterruptedToolEnvelopeAsUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	model := fakes.NewHistoryModel()
	engine := chat.NewWithHistory(ctx, model, fakes.NewCommander(bridge.Operation{}), []llm.Message{
		{Role: "user", Text: "Run both"},
		{Role: "assistant", Calls: []llm.Call{
			{ID: "first", Name: "first_action", Arguments: json.RawMessage(`{}`)},
			{ID: "second", Name: "second_action", Arguments: json.RawMessage(`{}`)},
		}},
		{Role: "tool", CallID: "first", Name: "first_action", Text: "First completed"},
	})
	defer engine.Close()

	if err := engine.Send("Inspect current state"); err != nil {
		t.Fatal(err)
	}
	var restored []llm.Message
	select {
	case restored = <-model.Requests():
	case <-ctx.Done():
		t.Fatal("model did not receive restored context")
	}

	if len(restored) != 5 || restored[2].Text != "First completed" || restored[3].CallID != "second" ||
		!strings.Contains(restored[3].Text, "execution outcome is unknown") || restored[4].Role != "user" {
		t.Fatalf("interrupted tool envelope was not recovered: %+v", restored)
	}
	for _, event := range toolStatusEventsUntil(t, ctx, engine, "idle", 1) {
		if event.Kind == "tool" {
			t.Fatal("an interrupted saved tool was retried")
		}
	}
}

func TestChatClearPersistsEmptyModelContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	model := fakes.NewHistoryModel()
	engine := chat.NewWithHistory(ctx, model, fakes.NewCommander(bridge.Operation{}), []llm.Message{{Role: "user", Text: "Old context"}})
	defer engine.Close()

	if err := engine.Send("/clear"); err != nil {
		t.Fatal(err)
	}
	events := toolStatusEventsUntil(t, ctx, engine, "notice", 1)
	if len(events) < 2 || events[0].Kind != "history" || len(events[0].History) != 0 {
		t.Fatalf("cleared context was not saved: %+v", events)
	}
	if err := engine.Send("New context"); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-model.Requests():
		if len(received) != 1 || received[0].Role != "user" || received[0].Text != "New context" {
			t.Fatalf("old context survived clear: %+v", received)
		}
	case <-ctx.Done():
		t.Fatal("model did not receive new context")
	}
}

func TestChatRestoresToolsAsHistoricalTextForAnotherModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	previous := []llm.Message{
		{Role: "user", Text: "Install the agent"},
		{Role: "assistant", Text: "Installing", Native: json.RawMessage(`{"provider":"specific"}`),
			Calls: []llm.Call{
				{ID: "saved-call", Name: "install_agent", Arguments: json.RawMessage(`{"source":"approved-source"}`)},
				{ID: "interrupted-call", Name: "inspect_agent", Arguments: json.RawMessage(`{}`)},
			}},
		{Role: "tool", CallID: "saved-call", Name: "install_agent", Text: "Agent installed"},
	}
	model := fakes.NewHistoryModel()
	engine := chat.NewWithHistory(ctx, model, fakes.NewCommander(bridge.Operation{}), chat.PortableHistory(previous))
	defer engine.Close()

	if err := engine.Send("What happened earlier?"); err != nil {
		t.Fatal(err)
	}
	var received []llm.Message
	select {
	case received = <-model.Requests():
	case <-ctx.Done():
		t.Fatal("model did not receive portable context")
	}

	if len(received) != 5 || !strings.Contains(received[1].Text, "Historical tool requests") ||
		!strings.Contains(received[1].Text, "approved-source") || !strings.Contains(received[2].Text, "Historical tool result for install_agent") ||
		!strings.Contains(received[2].Text, "Agent installed") || !strings.Contains(received[3].Text, "execution outcome is unknown") {
		t.Fatalf("historical tool evidence was lost: %+v", received)
	}
	for _, message := range received {
		if len(message.Native) != 0 || len(message.Calls) != 0 || message.Role == "tool" || message.CallID != "" {
			t.Fatal("provider-specific tool envelope survived portable restoration")
		}
	}
	if len(previous[1].Native) == 0 || len(previous[1].Calls) == 0 || previous[2].Role != "tool" {
		t.Fatal("portable restoration mutated its saved snapshot")
	}
	for _, event := range toolStatusEventsUntil(t, ctx, engine, "idle", 1) {
		if event.Kind == "tool" {
			t.Fatal("portable context replayed a historical call")
		}
	}
}

func TestChatCloseRetainsActiveToolOutcomeWithoutEventDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	calls := fixtures.ToolStatusCalls()[:1]
	commander := fakes.NewToolStatusCommander(fixtures.ToolStatusName, fixtures.ToolStatusReplies())
	engine := chat.New(ctx, fakes.NewToolStatusModel(calls, "Inspecting", "Inspected"), commander)
	defer engine.Close()
	if err := engine.Send("Inspect once"); err != nil {
		t.Fatal(err)
	}
	toolStatusEventsUntil(t, ctx, engine, "tool", 1)

	saved := engine.CloseWithHistory()

	if len(saved) != 3 || saved[0].Text != "Inspect once" || len(saved[1].Calls) != 1 ||
		saved[2].Role != "tool" || saved[2].CallID != calls[0].ID || !strings.Contains(saved[2].Text, "inspect state before retrying") {
		t.Fatalf("active tool evidence was lost during close: %+v", saved)
	}
	model := fakes.NewHistoryModel()
	restored := chat.NewWithHistory(ctx, model, fakes.NewCommander(bridge.Operation{}), saved)
	defer restored.Close()
	if err := restored.Send("/status"); err != nil {
		t.Fatal(err)
	}
	waitStopEvent(t, ctx, restored, "notice")
	select {
	case <-model.Requests():
		t.Fatal("closing and reopening retried the interrupted tool")
	default:
	}
}
