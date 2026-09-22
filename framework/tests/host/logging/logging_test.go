package logging_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"commander/framework/host"
)

func TestLogFormatsPreserveFieldsAndFilterLevels(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			// Arrange.
			var output bytes.Buffer
			logger, err := host.NewLogger(&output, "warn", format)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			logger.Debug("heartbeat")
			logger.Info("agent started")
			logger.Warn("agent unavailable", "agent_id", "example")

			// Assert.
			if strings.Count(output.String(), "\n") != 1 {
				t.Fatalf("expected one warning log: %s", output.String())
			}
			if format == "json" {
				var entry map[string]any
				if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
					t.Fatalf("invalid JSON log: %v", err)
				}
				if entry["msg"] != "agent unavailable" || entry["agent_id"] != "example" || entry["level"] != "WARN" {
					t.Fatalf("unexpected log: %+v", entry)
				}
			} else {
				for _, field := range []string{`level=WARN`, `msg="agent unavailable"`, `agent_id=example`} {
					if !strings.Contains(output.String(), field) {
						t.Fatalf("log lacks %s: %s", field, output.String())
					}
				}
			}
		})
	}
}

func TestNewDoesNotChangeTheGlobalLogger(t *testing.T) {
	// Arrange.
	before := slog.Default()

	// Act.
	logger, err := host.NewLogger(io.Discard, "info", "json")

	// Assert.
	if err != nil || logger == nil {
		t.Fatalf("create logger: %v", err)
	}
	if slog.Default() != before {
		t.Fatal("creating a process logger replaced the global logger")
	}
}

func TestInvalidLoggingConfigurationIsRejected(t *testing.T) {
	for _, test := range []struct{ name, level, format string }{
		{"unknown level", "verbose", "json"},
		{"unknown format", "info", "xml"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			var output bytes.Buffer

			// Act.
			logger, err := host.NewLogger(&output, test.level, test.format)

			// Assert.
			if err == nil || logger != nil || output.Len() != 0 {
				t.Fatalf("invalid configuration produced logger=%v output=%q error=%v", logger, output.String(), err)
			}
		})
	}
}
