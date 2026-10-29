package playground_test

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestUIRestoresConversationWithoutRepeatingStartup(t *testing.T) {
	u, store := fixtures.RemoteHistoryUI(t, fixtures.StartupHistory())

	view := u.Model.View()

	for _, current := range []string{"Other (OpenAI-compatible) · fixture", "Commander connected · http://commander.history.test/mcp",
		"Try: Install the web agent, then review defos.ai.", "Saved conversation restored."} {
		if strings.Count(view, current) != 1 {
			t.Fatalf("startup entry %q was absent or repeated: %s", current, view)
		}
	}
	for _, previous := range []string{"OpenAI · chat-latest", "http://mcp:4273/mcp", "saved for agents.", "existing value kept.", "all-in-one"} {
		if strings.Contains(view, previous) {
			t.Fatalf("restored previous startup entry %q: %s", previous, view)
		}
	}
	for _, retained := range []string{"Earlier request", "Earlier reply", "inspect completed", "Saved operation available", "Admin state retained", "Commander connected · operator note"} {
		if !strings.Contains(view, retained) {
			t.Fatalf("conversation entry %q was lost: %s", retained, view)
		}
	}
	u.Close()
	saved := store.Snapshot()
	for _, item := range saved.Entries {
		if item.Text == "Other (OpenAI-compatible) · fixture" || strings.HasPrefix(item.Text, "Saved conversation restored.") ||
			strings.HasPrefix(item.Text, "Try: Install the web agent") || item.Text == "Commander connected · http://commander.history.test/mcp" {
			t.Fatalf("current startup entry was persisted: %+v", item)
		}
	}
	if len(saved.Messages) != 2 || saved.Messages[0].Text != "Earlier request" || saved.Messages[1].Text != "Earlier reply" {
		t.Fatal("removing repeated startup changed the model conversation")
	}

	restarted, _ := fixtures.RemoteHistoryUI(t, saved)

	if strings.Count(restarted.Model.View(), "Commander connected · http://commander.history.test/mcp") != 1 ||
		strings.Count(restarted.Model.View(), "Saved conversation restored.") != 1 {
		t.Fatalf("a subsequent restart repeated startup: %s", restarted.Model.View())
	}
}

func TestUIRestoresTranscriptAndSavesWithoutCredentials(t *testing.T) {
	u, store := fixtures.HistoryUI(t, ui.History{Version: 1,
		Messages: []llm.Message{{Role: "user", Text: "Earlier request"}, {Role: "assistant", Text: "Earlier reply"}},
		Entries: []ui.HistoryEntry{{Kind: "user", Text: "Earlier request"}, {Kind: "assistant", Text: "Earlier reply"},
			{Kind: "tool", Text: "inspect", CallID: "interrupted-call"}},
	})
	if view := u.Model.View(); !strings.Contains(view, "Earlier reply") || !strings.Contains(view, "interrupted; inspect Commander state") {
		t.Fatalf("saved transcript was not restored: %s", view)
	}

	u.Text("/credentials")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Text("Continue")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("new reply", func(view string) bool { return strings.Contains(view, "New reply") })
	u.Close()

	snapshot := store.Snapshot()
	if snapshot.Version != 1 || len(snapshot.Messages) < 3 || snapshot.Messages[0].Text != "Earlier request" {
		t.Fatalf("model context was not retained: %+v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"provider-key-must-not-be-saved", "api-password-must-not-be-saved", "mcp-token-must-not-be-saved"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("credentials leaked into saved chat history")
		}
	}
	if !strings.Contains(string(encoded), "Earlier reply") || !strings.Contains(string(encoded), "New reply") || store.URL == "" {
		t.Fatal("visible transcript or connected endpoint was not retained")
	}
}

func TestUILeavesUnsupportedHistoryUnchanged(t *testing.T) {
	u, store := fixtures.HistoryUI(t, ui.History{Version: 99, Entries: []ui.HistoryEntry{{Kind: "assistant", Text: "Unsupported history"}}})
	u.Close()

	if store.Snapshot().Version != 0 {
		t.Fatal("unsupported saved history was overwritten")
	}
	if view := u.Model.View(); !strings.Contains(view, "left unchanged") || strings.Contains(view, "Unsupported history") {
		t.Fatalf("unsupported history was not reported safely: %s", view)
	}
}

func TestUIReportsHistoryFailureWhileIdleAndOnClose(t *testing.T) {
	u := fixtures.FailingHistoryUI(t)

	u.Wait("history write failure", func(view string) bool {
		return strings.Contains(view, "Chat history could not be saved")
	})
	err := u.Model.Close()

	if err == nil || !strings.Contains(err.Error(), "chat history could not be saved") {
		t.Fatalf("failed final save was not reported on close: %v", err)
	}
}
