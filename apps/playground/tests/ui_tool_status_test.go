package playground_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestUIToolCallReplacesPendingStatus(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "completed"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			replies := fixtures.ToolStatusReplies()
			replies["first"] = fakes.ToolStatusReply{Result: bridge.Result{IsError: failed, Text: "fixture tool detail"}}
			u, commander := fixtures.ToolStatusUI(t, ctx, fixtures.ToolStatusCalls()[:1], replies)

			u.Text("Check tools")
			u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			u.Wait("pending tool row", func(view string) bool { return strings.Contains(view, "↳  "+fixtures.ToolStatusName) })
			assertToolStatusHeight(t, u.Model.View(), 40)
			u.Text("/unknown-tool-status-command")
			u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			u.Wait("unrelated error", func(view string) bool { return strings.Contains(view, "Unknown command. Type /help.") })
			commander.Release("first")
			u.Wait("terminal tool row", toolStatusReplyFinished)

			view := u.Model.View()
			assertToolStatusHeight(t, view, 40)
			if strings.Count(view, fixtures.ToolStatusName) != 1 || strings.Contains(view, "↳") {
				t.Fatalf("pending and terminal tool rows were duplicated: %s", view)
			}
			if !strings.Contains(view, fixtures.ToolStatusName+" "+name) ||
				strings.Count(view, "Unknown command. Type /help.") != 1 || !strings.Contains(view, "Commander connected") {
				t.Fatalf("replacement lost its outcome or unrelated notices: %s", view)
			}
			if failed && !strings.Contains(view, "fixture tool detail") {
				t.Fatalf("failed tool detail was lost: %s", view)
			}
			assertToolStatusStreamOrder(t, view)
		})
	}
}

func TestUIRepeatedToolNamesKeepSeparateOutcomeRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	u, commander := fixtures.ToolStatusUI(t, ctx, fixtures.ToolStatusCalls(), fixtures.ToolStatusReplies())

	u.Text("Check both tools")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("first pending tool", func(view string) bool { return strings.Contains(view, "↳  "+fixtures.ToolStatusName) })
	commander.Release("first")
	u.Wait("completed first and pending second", func(view string) bool {
		return strings.Contains(view, fixtures.ToolStatusName+" completed") && strings.Contains(view, "↳  "+fixtures.ToolStatusName)
	})
	commander.Release("second")
	u.Wait("both terminal tools", toolStatusReplyFinished)

	view := u.Model.View()
	if strings.Count(view, fixtures.ToolStatusName) != 2 || strings.Contains(view, "↳") ||
		strings.Count(view, fixtures.ToolStatusName+" completed") != 1 || strings.Count(view, fixtures.ToolStatusName+" failed") != 1 {
		t.Fatalf("same-name calls did not retain independent outcome rows: %s", view)
	}
	if strings.Index(view, fixtures.ToolStatusName+" completed") > strings.Index(view, fixtures.ToolStatusName+" failed") {
		t.Fatalf("tool completion changed transcript order: %s", view)
	}
	assertToolStatusStreamOrder(t, view)
}

func TestUIToolStatusReplacementReflowsWithViewport(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	replies := fixtures.ToolStatusReplies()
	replies["first"] = fakes.ToolStatusReply{Result: bridge.Result{IsError: true, Text: "first detail line\n" + strings.Repeat("wrapped failure detail ", 8)}}
	u, commander := fixtures.ToolStatusUI(t, ctx, fixtures.ToolStatusCalls()[:1], replies)

	u.Model.Update(tea.WindowSizeMsg{Width: 52, Height: 60})
	u.Text("Check wrapping")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("pending tool before reflow", func(view string) bool { return strings.Contains(view, "↳  "+fixtures.ToolStatusName) })
	commander.Release("first")
	u.Wait("wrapped failure", toolStatusReplyFinished)

	for _, width := range []int{100, 34, 52} {
		u.Model.Update(tea.WindowSizeMsg{Width: width, Height: 60})
		view := u.Model.View()
		assertToolStatusHeight(t, view, 60)
		text := strings.Join(strings.Fields(view), " ")
		if strings.Count(view, fixtures.ToolStatusName) != 1 || strings.Contains(view, "↳") ||
			!strings.Contains(text, "first detail line") || !strings.Contains(text, "wrapped failure detail") {
			t.Fatalf("width %d retained stale pending render or lost detail: %s", width, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d overflowed after replacement: %q", width, line)
			}
		}
	}
}

func TestUIConcurrentSameNameCallsReplaceTheirOwnRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	calls := fixtures.ToolStatusCalls()[:1]
	calls[0].Name = "list_operations"
	replies := fixtures.ToolStatusReplies()
	replies[""] = fakes.ToolStatusReply{Result: bridge.Result{IsError: true, Text: "operator control detail"}}
	u, commander := fixtures.ToolStatusUI(t, ctx, calls, replies)

	u.Text("Read operations through the model")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("model tool pending", func(view string) bool { return strings.Contains(view, "↳  list_operations") })
	u.Text("/operations")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("two concurrent tools pending", func(view string) bool { return strings.Count(view, "↳  list_operations") == 2 })
	commander.Release("")
	u.Wait("operator tool failed before model tool", func(view string) bool {
		return strings.Contains(view, "operator control detail") && strings.Contains(view, "↳  list_operations")
	})

	view := u.Model.View()
	if strings.Index(view, "↳  list_operations") > strings.Index(view, "list_operations failed") {
		t.Fatalf("operator failure replaced the model's earlier pending row: %s", view)
	}

	commander.Release("first")
	u.Wait("model tool completed", toolStatusReplyFinished)

	view = u.Model.View()
	if strings.Count(view, "list_operations") != 2 || strings.Contains(view, "↳") ||
		strings.Index(view, "list_operations completed") > strings.Index(view, "list_operations failed") {
		t.Fatalf("concurrent outcomes lost their original row positions: %s", view)
	}
}

func toolStatusReplyFinished(view string) bool {
	return strings.Contains(view, "After tool calls") && !strings.Contains(view, "Ctrl+C stop reply")
}

func assertToolStatusStreamOrder(t *testing.T, view string) {
	t.Helper()
	before, tool, after := strings.Index(view, "Before tool calls"), strings.Index(view, fixtures.ToolStatusName), strings.Index(view, "After tool calls")
	if before < 0 || tool <= before || after <= tool {
		t.Fatalf("tool replacement reordered or lost streamed text: %s", view)
	}
}

func assertToolStatusHeight(t *testing.T, view string, height int) {
	t.Helper()
	if lines := strings.Count(view, "\n"); lines != height-1 {
		t.Fatalf("tool status viewport height = %d, want %d: %s", lines, height-1, view)
	}
}
