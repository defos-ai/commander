package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestModelToolRoundBoundsOutputAndCompactsEvidenceWithoutLosingProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	model, commander, fake := fixtures.NewModelOptimization(t, ctx)
	history := []llm.Message{{Role: "user", Text: "Inspect the current session."}}

	first, err := model.Complete(ctx, "Use tool evidence.", history, commander.Tools(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Calls) != 1 {
		t.Fatalf("expected one completed tool call, got %d", len(first.Calls))
	}
	call := first.Calls[0]
	result, err := commander.Call(ctx, call.Name, call.Arguments)
	if err != nil || result.IsError {
		t.Fatalf("inspect failed: %v", err)
	}
	history = append(history, first, llm.Message{Role: "tool", CallID: call.ID, Name: call.Name, Text: result.Text})
	final, err := model.Complete(ctx, "Use tool evidence.", history, commander.Tools(), nil)
	if err != nil {
		t.Fatal(err)
	}

	requests, runs, original := fake.Snapshot()
	if final.Text != "Inspection finished." || len(final.Calls) != 0 || runs != 1 || len(requests) != 2 {
		t.Fatalf("unexpected tool round: reply=%q calls=%d tool runs=%d requests=%d", final.Text, len(final.Calls), runs, len(requests))
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, original); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Data, original) || result.Text != compact.String() || len(result.Text) >= len(original) {
		t.Fatal("model context must compact JSON while retaining the original tool evidence")
	}
	for _, body := range requests {
		var request struct {
			MaxOutputTokens int               `json:"max_output_tokens"`
			Input           []json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request.MaxOutputTokens != 16384 {
			t.Fatalf("unexpected output allocation: %d", request.MaxOutputTokens)
		}
	}
	var second struct {
		Input []struct {
			Type             string `json:"type"`
			CallID           string `json:"call_id"`
			EncryptedContent string `json:"encrypted_content"`
			Output           string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(requests[1], &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Input) != 4 || second.Input[1].EncryptedContent != "fixture-encrypted-reasoning" || second.Input[2].CallID != call.ID || second.Input[3].Type != "function_call_output" || second.Input[3].CallID != call.ID || second.Input[3].Output != compact.String() {
		t.Fatal("compaction must preserve reasoning, tool identity, and exact JSON string and numeric values")
	}
}
