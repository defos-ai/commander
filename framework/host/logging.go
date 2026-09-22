package host

import (
	"fmt"
	"io"
	"log/slog"
)

// NewLogger creates a logger for stderr and container log collectors without
// changing the application's global logger.
func NewLogger(writer io.Writer, level, format string) (*slog.Logger, error) {
	var severity slog.Level
	switch level {
	case "debug":
		severity = slog.LevelDebug
	case "info":
		severity = slog.LevelInfo
	case "warn":
		severity = slog.LevelWarn
	case "error":
		severity = slog.LevelError
	default:
		return nil, fmt.Errorf("invalid log level %q; use debug, info, warn, or error", level)
	}
	if writer == nil {
		writer = io.Discard
	}
	options := &slog.HandlerOptions{Level: severity}
	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(writer, options)
	case "text":
		handler = slog.NewTextHandler(writer, options)
	default:
		return nil, fmt.Errorf("invalid log format %q; use json or text", format)
	}
	return slog.New(handler), nil
}
