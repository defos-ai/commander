package local_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"commander/apps/playground/internal/local"
	bridge "commander/apps/playground/internal/mcp"
)

func TestFailedLocalStartupRemovesTemporaryState(t *testing.T) {
	for _, failure := range []string{"canceled caller", "invalid agent environment"} {
		t.Run(failure, func(t *testing.T) {
			// Arrange: force a real startup failure without replacing any host.
			temporary := t.TempDir()
			t.Setenv("TMPDIR", temporary)
			t.Setenv("SERVER_IP_ADDRESS", "")
			t.Setenv("SERVER_HOSTNAME", "")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if failure == "canceled caller" {
				cancel()
			} else {
				t.Setenv("SERVER_IP_ADDRESS", "not-an-ip")
			}

			// Act: the invalid agent fails after the controller has started.
			session, err := local.Start(ctx)
			if session != nil {
				defer session.Close()
				t.Fatal("failed startup returned a running session")
			}

			// Assert: cancellation and startup errors are returned after cleanup.
			if failure == "canceled caller" && !errors.Is(err, context.Canceled) {
				t.Fatalf("missing cancellation: %v", err)
			}
			if failure == "invalid agent environment" && (err == nil || !strings.Contains(err.Error(), "SERVER_IP_ADDRESS")) {
				t.Fatalf("missing startup cause: %v", err)
			}
			entries, readErr := os.ReadDir(temporary)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("failed startup left temporary files: %v (%v)", entries, readErr)
			}
		})
	}
}

func TestLocalHostExitCancelsSessionAndReleasesResources(t *testing.T) {
	// Arrange: a real installation still starts when browser actions are
	// unavailable, explaining exactly which optional dependency is missing.
	t.Setenv("SERVER_IP_ADDRESS", "")
	t.Setenv("SERVER_HOSTNAME", "")
	t.Setenv("CHROMIUM_PATH", filepath.Join(t.TempDir(), "not-installed"))
	t.Setenv("CHROMIUM_NO_SANDBOX", "false")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	session, err := local.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, action := range []string{"check-accessibility", "capture-page"} {
		if !strings.Contains(strings.Join(session.Notices, "\n"), action+" unavailable: Install Chromium") {
			t.Fatalf("missing dependency notice for %s: %v", action, session.Notices)
		}
	}
	commander, err := bridge.Connect(ctx, session.MCPURL)
	if err != nil {
		t.Fatal(err)
	}
	defer commander.Close()
	result, err := commander.Call(ctx, "list_agents", json.RawMessage(`{}`))
	if err != nil || result.IsError {
		t.Fatalf("list_agents: %v %s", err, result.Text)
	}
	var inventory struct{ Agents []struct{ ID string } }
	if err := json.Unmarshal(result.Data, &inventory); err != nil || len(inventory.Agents) != 1 {
		t.Fatalf("expected one agent: %+v (%v)", inventory, err)
	}

	// Act: one of the supervised hosts exits through its actual MCP stop command.
	arguments, _ := json.Marshal(map[string]string{"agent_id": inventory.Agents[0].ID})
	_, _ = commander.Call(ctx, "stop_agent", arguments)
	select {
	case <-session.Context.Done():
	case <-ctx.Done():
		t.Fatal("agent exit did not cancel the local session")
	}
	cause := context.Cause(session.Context)
	if cause == nil || !strings.Contains(cause.Error(), "local agent") || !strings.Contains(cause.Error(), "stopped unexpectedly") {
		t.Fatalf("host exit cause=%v", cause)
	}
	_ = commander.Close()
	closeErr := session.Close()
	if closeErr == nil || !strings.Contains(closeErr.Error(), "stop agent: stopped unexpectedly") {
		t.Fatalf("shutdown lost host exit error: %v", closeErr)
	}
	if err := session.Close(); err != closeErr {
		t.Fatalf("repeated close changed the result: %v", err)
	}

	// Assert: graceful close waits for every host before deleting state.
	if _, err := os.Stat(session.Directory); !os.IsNotExist(err) {
		t.Fatalf("session directory still exists: %s (%v)", session.Directory, err)
	}
	parsed, err := url.Parse(session.MCPURL)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("host still owns %s: %v", session.MCPURL, err)
	}
	_ = listener.Close()
}
