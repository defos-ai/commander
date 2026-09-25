package missionsmcp

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func requestLogging(logger *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			started := time.Now()
			result, err := next(ctx, method, request)
			attributes := []any{"method", method, "duration", time.Since(started)}
			if params, ok := request.GetParams().(*mcp.CallToolParamsRaw); ok {
				attributes = append(attributes, "tool", params.Name)
			}
			failed := err != nil
			if toolResult, ok := result.(*mcp.CallToolResult); ok && toolResult != nil {
				failed = failed || toolResult.IsError
			}
			attributes = append(attributes, "failed", failed)
			level := slog.LevelInfo
			if failed {
				level = slog.LevelError
				attributes = append(attributes, "error", requestError(result, err))
			}
			logger.Log(ctx, level, "MCP request completed", attributes...)
			return result, err
		}
	}
}

func requestError(result mcp.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	if result, ok := result.(*mcp.CallToolResult); ok && result != nil && result.IsError {
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok && text.Text != "" {
				return text.Text
			}
		}
	}
	return "MCP request failed without an error message"
}
