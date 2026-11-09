package fakes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ModelOptimization struct {
	mu       sync.Mutex
	requests [][]byte
	toolRuns int
	result   []byte
}

func NewModelOptimization() *ModelOptimization {
	// Preserve the large integer while adding whitespace to the tool response.
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"session_id":"fixture-session","finding":9007199254740993,"evidence":{"text":"keep  spaces\nand lines","status":"ready"}}`), &raw)
	data, _ := json.MarshalIndent(raw, "", "                ")
	return &ModelOptimization{result: data}
}

func (f *ModelOptimization) MCP() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "optimization-fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "inspect", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		f.mu.Lock()
		f.toolRuns++
		f.mu.Unlock()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(f.result)}}}, nil
	})
	return server
}

func (f *ModelOptimization) Model(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/responses" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read model request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, body)
	request := len(f.requests)
	f.mu.Unlock()
	output := json.RawMessage(`[{"type":"reasoning","id":"reasoning-1","encrypted_content":"fixture-encrypted-reasoning"},{"type":"function_call","call_id":"inspect-1","name":"inspect","arguments":"{}"}]`)
	if request > 1 {
		output = json.RawMessage(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Inspection finished."}]}]`)
	}
	event, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": output}})
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "data: "+string(event)+"\n\n")
}

func (f *ModelOptimization) Snapshot() ([][]byte, int, []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	requests := make([][]byte, len(f.requests))
	for i, request := range f.requests {
		requests[i] = append([]byte(nil), request...)
	}
	return requests, f.toolRuns, append([]byte(nil), f.result...)
}
