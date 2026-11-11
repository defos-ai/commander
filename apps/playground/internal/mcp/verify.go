package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/framework/host"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Verification struct {
	ToolCount int `json:"tool_count"`
}

func Verify(ctx context.Context, endpoint string, credentials host.BearerCredentials) (Verification, error) {
	if err := ctx.Err(); err != nil {
		return Verification{}, err
	}
	if err := llm.ValidateURL(endpoint); err != nil {
		return Verification{}, err
	}
	if err := credentials.Validate(); err != nil {
		return Verification{}, errors.New("a valid Commander MCP token is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := protocol.NewClient(&protocol.Implementation{Name: "commander-setup", Version: "0.5.0"}, &protocol.ClientOptions{Logger: logger})
	httpClient := &http.Client{
		Transport:     &bearerTransport{credentials: credentials},
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	session, err := client.Connect(ctx, &protocol.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: httpClient, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return Verification{}, verificationError(ctx, err, "cannot initialize Commander MCP; check its URL and availability")
	}
	defer session.Close()
	verification, err := verifyTools(ctx, session)
	if err != nil {
		return Verification{}, err
	}
	result, err := session.CallTool(ctx, &protocol.CallToolParams{Name: "list_agents", Arguments: map[string]any{}})
	if err != nil {
		return Verification{}, verificationError(ctx, err, "Commander MCP read-only verification failed")
	}
	if result == nil || result.IsError {
		return Verification{}, errors.New("Commander MCP read-only verification failed")
	}
	return verification, nil
}

func verifyTools(ctx context.Context, session *protocol.ClientSession) (Verification, error) {
	var verification Verification
	seen := map[string]bool{"": true}
	available := map[string]bool{}
	cursor := ""
	for {
		page, err := session.ListTools(ctx, &protocol.ListToolsParams{Cursor: cursor})
		if err != nil {
			return Verification{}, verificationError(ctx, err, "could not discover Commander MCP tools")
		}
		for _, tool := range page.Tools {
			if tool == nil || tool.Name == "" {
				return Verification{}, errors.New("Commander MCP returned an invalid tool inventory")
			}
			available[tool.Name] = true
			verification.ToolCount++
		}
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return Verification{}, errors.New("Commander MCP returned a repeated tool cursor")
		}
		cursor = page.NextCursor
		seen[cursor] = true
	}
	if !available["list_agents"] || !available["list_missions"] {
		return Verification{}, errors.New("MCP endpoint does not expose Commander's required tools")
	}
	return verification, nil
}

func verificationError(ctx context.Context, err error, message string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, errAuthentication) {
		return errAuthentication
	}
	return errors.New(message)
}
