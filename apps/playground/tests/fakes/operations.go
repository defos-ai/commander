package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type OperationServer struct {
	t       testing.TB
	server  *mcp.Server
	current atomic.Pointer[operationSnapshot]
	reads   atomic.Int32
}

type operationSnapshot struct {
	records map[string]string
	latest  string
}

func NewOperationServer(t testing.TB, latestOnly bool, private ...string) *OperationServer {
	t.Helper()
	operations := &OperationServer{t: t}
	operations.current.Store(&operationSnapshot{records: map[string]string{}})
	operations.server = mcp.NewServer(&mcp.Implementation{Name: "operation-fixture", Version: "1"}, nil)
	for _, name := range []string{"get_operation", "operation_events"} {
		operations.server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			assertPrivate(t, request.Params.Arguments, private)
			payload := `{"events":[],"next_cursor":""}`
			if request.Params.Name == "get_operation" {
				payload = ""
				snapshot := operations.current.Load()
				if latestOnly {
					payload = snapshot.latest
				} else {
					var args struct {
						ID string `json:"operation_id"`
					}
					if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
						return nil, err
					}
					payload = snapshot.records[args.ID]
				}
				operations.reads.Add(1)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: payload}}}, nil
		})
	}
	return operations
}

func (s *OperationServer) Publish(operation bridge.Operation) {
	s.t.Helper()
	encoded, err := json.Marshal(operation)
	if err != nil {
		s.t.Fatal(err)
	}
	payload := string(encoded)
	previous := s.current.Load()
	records := make(map[string]string, len(previous.records)+1)
	for id, saved := range previous.records {
		records[id] = saved
	}
	records[operation.ID] = payload
	s.current.Store(&operationSnapshot{records: records, latest: payload})
}

func (s *OperationServer) ReadCount() int32 { return s.reads.Load() }

func (s *OperationServer) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.server }, nil)
}

func AdminReview(t testing.TB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error("could not inspect model request")
		}
		if strings.Contains(string(body), "Review on Admin UI") || strings.Contains(string(body), "http://localhost:4275") {
			t.Error("Admin link reached model history")
		}
		WriteModelDelta(w, map[string]any{"content": "Operation reviewed"}, "stop")
	})
}

func SecretReview(t testing.TB, private ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error("could not inspect model request")
		}
		assertPrivate(t, body, private)
		content := "Operation reviewed"
		if bytes.Contains(body, []byte("Normal message after")) {
			content = "Normal chat remains available"
		}
		var payload struct{ Messages []struct{ Content string } }
		if err := json.Unmarshal(body, &payload); err == nil && len(payload.Messages) != 0 {
			last := payload.Messages[len(payload.Messages)-1].Content
			if strings.HasPrefix(last, "Operation update:") && strings.Contains(last, `"ready":true`) {
				content = "Ready preflight reviewed"
			}
		}
		WriteModelDelta(w, map[string]any{"content": content}, "stop")
	})
}

func assertPrivate(t testing.TB, body []byte, private []string) {
	for _, value := range private {
		if bytes.Contains(body, []byte(value)) {
			t.Error("local secret form leaked private data outside the API")
		}
	}
}
