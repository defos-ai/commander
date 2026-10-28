package fixtures

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
)

const ToolStatusName = "inspect_tool"

func ToolStatusCalls() []llm.Call {
	return []llm.Call{
		{ID: "call-first", Name: ToolStatusName, Arguments: []byte(`{"key":"first"}`)},
		{ID: "call-second", Name: ToolStatusName, Arguments: []byte(`{"key":"second"}`)},
	}
}

func ToolStatusReplies() map[string]fakes.ToolStatusReply {
	return map[string]fakes.ToolStatusReply{
		"first":  {Result: bridge.Result{Text: `{"ready":true}`}},
		"second": {Result: bridge.Result{IsError: true, Text: "fixture tool detail"}},
	}
}

func ToolStatusUI(t *testing.T, ctx context.Context, calls []llm.Call, replies map[string]fakes.ToolStatusReply) (*UIDriver, *fakes.ToolStatusCommander) {
	t.Helper()
	commander := fakes.NewToolStatusCommander(calls[0].Name, replies)
	endpoint := httptest.NewServer(commander.Handler())
	t.Cleanup(endpoint.Close)
	provider := httptest.NewServer(fakes.NewToolStatusModel(calls, "Before tool calls", "After tool calls").Handler())
	t.Cleanup(provider.Close)
	t.Cleanup(commander.ReleaseAll)
	return NewUI(t, ctx, endpoint.URL, provider.URL), commander
}
