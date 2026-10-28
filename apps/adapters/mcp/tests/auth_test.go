package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestMCPDeploymentMetadata(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	endpoint := strings.TrimSuffix(fixtures.McpEndpoint(), "/mcp") + "/aboutz"
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request, err := http.NewRequestWithContext(t.Context(), method, endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("%s /aboutz: status=%d, error=%v", method, response.StatusCode, err)
		}
		if method == http.MethodHead {
			if len(body) != 0 {
				t.Error("HEAD /aboutz returned a body")
			}
			continue
		}
		var metadata map[string]string
		if err := json.Unmarshal(body, &metadata); err != nil {
			t.Fatal(err)
		}
		if len(metadata) != 2 || metadata["runtime"] != "docker-compose" || metadata["application"] != "commander-mcp" {
			t.Fatalf("unexpected MCP deployment metadata: %+v", metadata)
		}
	}
}

func TestUnauthorizedCleanupPreservesSavedWork(t *testing.T) {
	a := fixtures.Connect(t)
	op := a.Start("Saved cleanup fixture")
	a.Call("cancel_operation", fixtures.Object{"operation_id": op.ID}, nil)
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for _, authorization := range []string{"", "Bearer incorrect-test-token"} {
		request, err := http.NewRequestWithContext(a.Context, http.MethodPost, fixtures.McpEndpoint(), strings.NewReader(
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
		if got := a.Get(op.ID); got.ID != op.ID || got.Status != operations.Cancelled {
			t.Fatalf("unauthorized cleanup changed saved operation: id=%s status=%s", got.ID, got.Status)
		}
	}
	a.Call("clean_up_state", fixtures.Object{}, nil)
	var list struct {
		Operations []operations.Operation `json:"operations"`
	}
	a.Call("list_operations", fixtures.Object{}, &list)
	if len(list.Operations) != 0 {
		t.Fatal("authenticated cleanup did not remove saved operations")
	}
}
