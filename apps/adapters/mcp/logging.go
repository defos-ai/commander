package missionsmcp

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Log method names and outcomes without recording MCP tool arguments, results,
// credentials, or artifact contents.
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
			}
			logger.Log(ctx, level, "MCP request completed", attributes...)
			return result, err
		}
	}
}
