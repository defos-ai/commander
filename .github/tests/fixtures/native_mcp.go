package fixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

type NativeMCP struct {
	endpoint, token, protocol string
	client                    *http.Client
	nextID                    int
}

func ConnectNativeMCP(t *testing.T, endpoint, token string) *NativeMCP {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	t.Cleanup(transport.CloseIdleConnections)
	client := &NativeMCP{endpoint: endpoint, token: token, client: &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	data := client.request(t, "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "commander-native-onboarding", "version": "1"},
	}, false)
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(data, &initialized); err != nil || initialized.ProtocolVersion == "" {
		t.Fatal("MCP initialize did not negotiate a protocol version")
	}
	client.protocol = initialized.ProtocolVersion
	client.request(t, "notifications/initialized", nil, true)
	return client
}

func (c *NativeMCP) Call(t *testing.T, name string, args any) json.RawMessage {
	t.Helper()
	data := c.request(t, "tools/call", map[string]any{"name": name, "arguments": args}, false)
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.IsError {
		t.Fatalf("MCP tool %s did not succeed", name)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" || !json.Valid([]byte(result.Content[0].Text)) {
		t.Fatalf("MCP tool %s did not return one JSON text result", name)
	}
	return json.RawMessage(result.Content[0].Text)
}

func (c *NativeMCP) request(t *testing.T, method string, params any, notification bool) json.RawMessage {
	t.Helper()
	envelope := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		envelope["params"] = params
	}
	if !notification {
		c.nextID++
		envelope["id"] = c.nextID
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("encode MCP %s request: %v", method, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("create MCP %s request", method)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	if c.protocol != "" {
		request.Header.Set("MCP-Protocol-Version", c.protocol)
	}
	response, err := c.client.Do(request)
	if err != nil {
		t.Fatalf("MCP %s request failed (%T)", method, err)
	}
	defer response.Body.Close()
	if notification {
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("MCP %s returned HTTP %d", method, response.StatusCode)
		}
		return nil
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("MCP %s returned HTTP %d", method, response.StatusCode)
	}
	var reply struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&reply); err != nil {
		t.Fatalf("decode MCP %s response: %v", method, err)
	}
	if reply.Error != nil {
		t.Fatalf("MCP %s returned JSON-RPC error %d", method, reply.Error.Code)
	}
	if reply.JSONRPC != "2.0" || reply.ID != c.nextID || len(reply.Result) == 0 {
		t.Fatalf("MCP %s returned an invalid response envelope", method)
	}
	return reply.Result
}
