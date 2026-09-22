// Package fixture provides local protocol servers for playground acceptance tests.
package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tool func(context.Context, json.RawMessage) (any, error)

func MCP(t *testing.T, tools map[string]Tool) string {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, &mcp.ServerOptions{Instructions: "Use the fixture tools to inspect missions."})
	for name, run := range tools {
		server.AddTool(&mcp.Tool{Name: name, Description: "Fixture " + name, InputSchema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			value, err := run(ctx, request.Params.Arguments)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			data, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{StructuredContent: value, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil
		})
	}
	server.AddResource(&mcp.Resource{URI: "missions://artifacts/fixture", Name: "fixture"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "missions://artifacts/fixture", Text: "saved evidence"}}}, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

func SSE(w http.ResponseWriter, delta map[string]any, finish string) {
	w.Header().Set("Content-Type", "text/event-stream")
	choice := map[string]any{"delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	data, _ := json.Marshal(map[string]any{"choices": []any{choice}})
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}
