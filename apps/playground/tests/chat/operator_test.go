package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"commander/apps/playground/internal/chat"
	"commander/apps/playground/internal/llm"
	bridge "commander/apps/playground/internal/mcp"
	"commander/apps/playground/tests/fixture"
)

func TestOperatorRunsActionThroughMCP(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprintf("rejected=%t", reject), func(t *testing.T) {
			// Arrange: the controller validates the request; no model is needed.
			var calls atomic.Int32
			endpoint := fixture.MCP(t, map[string]fixture.Tool{
				"assign_task": func(_ context.Context, raw json.RawMessage) (any, error) {
					calls.Add(1)
					var args struct {
						OperationID string            `json:"operation_id"`
						ActionName  string            `json:"action_name"`
						Input       map[string]string `json:"input"`
					}
					if err := json.Unmarshal(raw, &args); err != nil || args.OperationID != "op-1" || args.ActionName != "inspect-page" || args.Input["url"] != "http://website/" {
						t.Errorf("unexpected action request: %s", raw)
					}
					if reject {
						return nil, errors.New("pending operator question")
					}
					return map[string]string{"id": "task-2", "status": "queued"}, nil
				},
				"get_operation": func(context.Context, json.RawMessage) (any, error) {
					return map[string]any{"id": "op-1", "status": "planning", "version": 1}, nil
				},
				"operation_events": func(ctx context.Context, _ json.RawMessage) (any, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				},
			})
			commander, err := bridge.Connect(t.Context(), endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer commander.Close()
			model := modelFunc(func(context.Context, []llm.Message, func(string)) (llm.Message, error) {
				t.Error("operator action must not invoke model")
				return llm.Message{}, errors.New("unexpected model call")
			})
			e := chat.New(t.Context(), model, commander)
			defer e.Close()

			// Act: invalid input stays local, valid input goes directly to MCP.
			_ = e.Send("/run op-1 inspect-page not-json")
			await(t, e, func(event chat.Event) bool { return event.Kind == "error" })
			if calls.Load() != 0 {
				t.Fatal("invalid input reached MCP")
			}
			_ = e.Send(`/run op-1 inspect-page {"url":"http://website/"}`)
			event := await(t, e, func(event chat.Event) bool { return event.Kind == "success" || event.Kind == "error" })

			// Assert: errors remain errors; no automatic override or retry.
			if calls.Load() != 1 {
				t.Fatalf("MCP calls=%d", calls.Load())
			}
			if reject && (event.Kind != "error" || !strings.Contains(event.Text, "pending operator question")) {
				t.Fatal(event)
			}
			if !reject && event.Kind != "success" {
				t.Fatal(event)
			}
		})
	}
}
