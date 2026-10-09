package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/host"
)

var errAuthentication = errors.New("Commander MCP authentication failed (HTTP 401); set COMMANDER_MCP_TOKEN to the server's token")

type bearerTransport struct {
	credentials host.BearerCredentials
	rejected    atomic.Bool
}

func (t *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.rejected.Load() {
		return nil, errAuthentication
	}
	request = request.Clone(request.Context())
	if t.credentials.Token != "" {
		request.Header.Set("Authorization", "Bearer "+t.credentials.Token)
	}
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized {
		// Suppress the SDK's fallback handshake after authentication is rejected.
		t.rejected.Store(true)
		response.Body.Close()
		return nil, errAuthentication
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Error bodies and redirect locations can echo authentication headers.
		response.Body.Close()
		return nil, fmt.Errorf("Commander MCP returned HTTP %d", response.StatusCode)
	}
	return response, nil
}
