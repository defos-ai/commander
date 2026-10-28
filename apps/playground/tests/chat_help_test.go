package playground_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/chat"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
)

func TestHelpCommandsStayLocalDuringIdleAndActiveReply(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("active=%t", active), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			model := fakes.NewReviewModel(active)
			engine := chat.New(ctx, model, fakes.NewCommander(bridge.Operation{}))
			defer engine.Close()
			if active {
				if err := engine.Send("Keep this reply running"); err != nil {
					t.Fatal(err)
				}
				select {
				case <-model.Started():
				case <-ctx.Done():
					t.Fatal("model did not start")
				}
			}

			for _, command := range []string{"/help", " /help\t "} {
				if err := engine.Send(command); err != nil {
					t.Fatal(err)
				}
				event := nextHelpEvent(t, ctx, engine)

				if event.Kind != "notice" || event.CallID != "" || event.ID != "" {
					t.Fatalf("local help produced a tool or operation status: %+v", event)
				}
				if event.Text != chat.Help || !strings.HasSuffix(event.Text, chat.HelpDocsURL) {
					t.Fatalf("compact help lost its documentation link: %q", event.Text)
				}
				assertCompactHelp(t, event.Text)
			}
			for _, command := range []string{"/help all", "/help other", "/help all extra", "/help ALL"} {
				if err := engine.Send(command); err != nil {
					t.Fatal(err)
				}
				event := nextHelpEvent(t, ctx, engine)

				if event.Kind != "error" || event.Text != "Usage: /help" || event.CallID != "" {
					t.Fatalf("invalid help reached model or MCP: %+v", event)
				}
			}
			select {
			case <-model.Started():
				t.Fatal("help invoked another model request")
			default:
			}
		})
	}
}

func nextHelpEvent(t *testing.T, ctx context.Context, engine *chat.Engine) chat.Event {
	t.Helper()
	for {
		select {
		case event, open := <-engine.Events():
			if !open {
				t.Fatal("chat closed before help was displayed")
			}
			if event.Kind == "tool" || event.Kind == "control_start" || event.Kind == "idle" {
				t.Fatalf("help dispatched work or stopped an active reply: %+v", event)
			}
			if event.Kind == "notice" || event.Kind == "error" {
				return event
			}
		case <-ctx.Done():
			t.Fatal("help was not handled while the engine remained available")
		}
	}
}

func assertCompactHelp(t *testing.T, help string) {
	t.Helper()
	for _, command := range []string{"/admin", "/credentials", "/secrets", "/operations", "/watch", "/stop", "/clear", "/exit"} {
		if !strings.Contains(help, command) {
			t.Fatalf("compact help omits %s: %q", command, help)
		}
	}
	for _, advanced := range []string{"/help all", "/run", "/cancel", "/status", "/answer", "PgUp/PgDown", "Ctrl+Shift+C", "Ctrl+D"} {
		if strings.Contains(help, advanced) {
			t.Fatalf("compact help includes advanced reference %s: %q", advanced, help)
		}
	}
}
