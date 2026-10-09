package mcp_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestUnauthorizedCleanupPreservesSavedWork(t *testing.T) {
	a := connect(t)
	op := a.start("https://example.test")
	a.call("cancel_operation", object{"operation_id": op.ID}, nil)
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for _, authorization := range []string{"", "Bearer incorrect-test-token"} {
		request, err := http.NewRequestWithContext(a.ctx, http.MethodPost, mcpEndpoint(), strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"clean_up_state","arguments":{}}}`,
		))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-06-18")
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthorized cleanup returned HTTP %d, want 401", response.StatusCode)
		}
		if got := a.get(op.ID); got.ID != op.ID || got.Status != operations.Cancelled {
			t.Fatalf("unauthorized cleanup changed saved operation: id=%s status=%s", got.ID, got.Status)
		}
	}
	a.call("clean_up_state", object{}, nil)
	var list struct {
		Operations []operations.Operation `json:"operations"`
	}
	a.call("list_operations", object{}, &list)
	if len(list.Operations) != 0 {
		t.Fatal("authenticated cleanup did not remove saved operations")
	}
}
