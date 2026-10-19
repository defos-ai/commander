package playground_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/framework/host"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAdminCommandDuringSetup(t *testing.T) {
	for _, test := range []struct {
		name, url, hint, want string
	}{
		{"configured", "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&username=commander", "", "Admin: http://localhost:4275/"},
		{"unavailable", "", "Admin is disabled (--no-admin).", "Admin is disabled (--no-admin)."},
		{"unconfigured", "", "", "Set --admin-url to the address of your Admin UI."},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := ui.New(t.Context(), ui.Config{AdminURL: test.url, AdminHint: test.hint, NoColor: true}, io.Discard)
			defer model.Close()
			model.Update(tea.WindowSizeMsg{Width: 180, Height: 60})
			submit := func(text string) {
				model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
				model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			}
			submit("/admin ignored")
			if !strings.Contains(model.View(), "Usage: /admin") || strings.Contains(model.View(), test.want) {
				t.Fatal("invalid Admin command did not show only its usage")
			}
			submit("/admin")
			if !strings.Contains(model.View(), test.want) || !strings.Contains(model.View(), "Select your model provider:") {
				t.Fatal("Admin command left setup or did not show its configured result")
			}
			submit("/back")
			submit("/help")
			if !strings.Contains(model.View(), "/admin — Show Admin UI link") {
				t.Fatal("setup help did not list Admin")
			}
			submit("/admin")
			if !strings.Contains(model.View(), test.want) {
				t.Fatal("Admin command stopped working after returning to setup")
			}
		})
	}
}

func TestAdminCommandStaysOutOfModelAndTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const adminURL = "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&username=commander"
	const password, token = "private-admin-password", "private-admin-token"
	var toolCalls, modelCalls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "admin-command-fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "list_operations", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolCalls.Add(1)
		return &mcp.CallToolResult{}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer endpoint.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("could not inspect model request")
		}
		for _, private := range []string{adminURL, "Usage: /admin", password, token} {
			if strings.Contains(string(body), private) {
				t.Error("local Admin command or credentials reached the model")
			}
		}
		var request struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		for _, message := range request.Messages {
			// General system guidance may mention /admin; local command input
			// and output must still stay out of the conversation.
			if message.Role != "system" && strings.Contains(message.Content, "/admin") {
				t.Error("local Admin command reached model history")
			}
		}
		writeModelDelta(w, map[string]any{"content": "Normal chat works"}, "stop")
	}))
	defer provider.Close()
	u := newUIWithConfig(t, ctx, ui.Config{
		Provider: "other", Model: "fixture", BaseURL: provider.URL, MCP: ui.Connection[host.BearerCredentials]{URL: endpoint.URL, Credentials: host.BearerCredentials{Token: token}},
		Keys: map[string]string{"other": "test-only"}, NoColor: true, AdminURL: adminURL,
		LocalAgentCount: 4,
		AllInOne:        true,
		API:             ui.Connection[host.BasicCredentials]{URL: "http://localhost:4274", Credentials: host.BasicCredentials{Username: "commander", Password: password}},
	})
	defer u.model.Close()
	u.model.Update(tea.WindowSizeMsg{Width: 180, Height: 80})
	if strings.Contains(u.model.View(), "Admin:") || strings.Contains(u.model.View(), adminURL) {
		t.Fatal("Admin link added noise to startup")
	}
	for _, command := range []string{"/admin unexpected", "/admin", "/admin", "/help"} {
		u.text(command)
		u.key(tea.KeyMsg{Type: tea.KeyEnter})
	}
	u.wait("help including Admin", func(view string) bool { return strings.Contains(view, "/admin — Show Admin UI link") })
	view := u.model.View()
	if !strings.Contains(view, "Usage: /admin") || strings.Count(view, "Admin: "+adminURL) != 2 {
		t.Fatal("Admin command is not repeatable or did not preserve its link")
	}
	if modelCalls.Load() != 0 || toolCalls.Load() != 0 {
		t.Fatal("Admin command or help called the model or MCP tools")
	}
	for _, private := range []string{password, token} {
		if strings.Contains(view, private) {
			t.Fatal("Admin command disclosed credentials")
		}
	}
	u.text("Hello")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("chat after Admin command", func(view string) bool {
		return strings.Contains(view, "Normal chat works") && !strings.Contains(view, "Thinking")
	})
	if modelCalls.Load() != 1 || toolCalls.Load() != 0 {
		t.Fatal("Admin command caused extra model or MCP calls")
	}
}

func TestAdminReviewLinksFollowCompletedOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const adminURL = "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&username=commander"
	u, publish, reads := adminOperationUI(t, ctx, adminURL, true)
	defer u.model.Close()
	u.model.Update(tea.WindowSizeMsg{Width: 300, Height: 100})
	command := func(text string) { u.text(text); u.key(tea.KeyMsg{Type: tea.KeyEnter}) }
	countLinks := func(want int) {
		t.Helper()
		if got := strings.Count(u.model.View(), "Review on Admin UI ("); got != want {
			t.Fatalf("Admin review link appeared %d times, want %d", got, want)
		}
		if strings.Contains(u.model.View(), "View results in Admin:") {
			t.Fatal("completion still adds a separate chat notice")
		}
	}
	command("/admin")
	countLinks(0)
	now := time.Now()
	for _, status := range []string{"failed", "cancelled"} {
		publish(bridge.Operation{ID: status, Status: status, UpdatedAt: now})
		command("/watch " + status)
		u.wait(status+" operation", func(view string) bool { return strings.Contains(view, status[:min(8, len(status))]+" · "+status) })
		countLinks(0)
	}
	op := bridge.Operation{ID: "target", Status: "running", UpdatedAt: now, Tasks: []bridge.Task{{ID: "task", Status: "succeeded"}}}
	publish(op)
	command("/watch target")
	u.wait("completed task in running operation", func(view string) bool { return strings.Contains(view, "target · running · 1/1 tasks finished") })
	countLinks(0)

	// Keep the watcher alive while delivering an obsolete terminal snapshot.
	op.Status, op.Tasks[0].Status = "completed", "running"
	op.UpdatedAt = now.Add(-time.Second)
	publish(op)
	targetReads := reads.Load() + 2
	u.wait("stale snapshot read", func(string) bool { return reads.Load() >= targetReads })
	op.Status, op.UpdatedAt = "waiting", now.Add(time.Second)
	publish(op)
	u.wait("fresh snapshot after stale completion", func(view string) bool { return strings.Contains(view, "target · waiting") })
	countLinks(0)

	op.Status, op.UpdatedAt = "completed", now.Add(2*time.Second)
	publish(op)
	u.wait("completion still draining", func(view string) bool { return strings.Contains(view, "target · completed · 0/1 tasks finished") })
	countLinks(0)
	op.UpdatedAt = now.Add(3 * time.Second)
	publish(op)
	targetReads = reads.Load() + 2
	u.wait("repeated completion snapshots", func(string) bool { return reads.Load() >= targetReads })
	op.Tasks[0].Status, op.UpdatedAt = "succeeded", now.Add(4*time.Second)
	publish(op)
	u.wait("settled completion", func(view string) bool {
		return strings.Contains(view, "target · completed · 1/1 tasks finished") && strings.Count(view, "Operation reviewed") == 3 && !strings.Contains(view, "Thinking")
	})
	countLinks(1)
	const targetURL = "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&operation=target&username=commander"
	if !strings.Contains(u.model.View(), "target · completed · 1/1 tasks finished · Review on Admin UI ("+targetURL+")") {
		t.Fatal("review link does not appear on its operation's progress row")
	}
	command("/clear")
	u.wait("cleared model history", func(view string) bool { return strings.Contains(view, "Model conversation cleared") })
	publish(bridge.Operation{ID: "second", Status: "completed", UpdatedAt: now})
	command("/watch second")
	u.wait("second completed operation", func(view string) bool { return strings.Contains(view, "second · completed") })
	countLinks(2)
	if !strings.Contains(u.model.View(), "operation=second&username=commander") || !strings.Contains(u.model.View(), "operation=target&username=commander") {
		t.Fatal("completed operations do not have distinct destinations")
	}
	command("/admin")
	if strings.Count(u.model.View(), "Admin: "+adminURL) != 2 {
		t.Fatal("Admin command did not remain available after operation completion")
	}
}

func TestAdminCompletionWithoutURLIsSilent(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	u, publish, _ := adminOperationUI(t, ctx, "", true)
	defer u.model.Close()
	publish(bridge.Operation{ID: "completed", Status: "completed", UpdatedAt: time.Now()})
	u.text("/watch completed")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("completion without Admin", func(view string) bool { return strings.Contains(view, "complete · completed") })
	if strings.Contains(u.model.View(), "Review on Admin UI") || strings.Contains(u.model.View(), "--admin-url") {
		t.Fatal("completion advertised an unknown Admin address")
	}
	u.text("/admin")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(u.model.View(), "Set --admin-url to the address of your Admin UI.") {
		t.Fatal("explicit Admin lookup did not explain missing configuration")
	}
}

func TestAdminLinkRendering(t *testing.T) {
	const adminURL = "http://localhost:4275/#/connect?api=http%3A%2F%2F127.0.0.1%3A4274&username=commander"
	for _, noColor := range []bool{false, true} {
		name := "terminal"
		if noColor {
			name = "plain-text"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			check := func(model *ui.Model, target string) {
				t.Helper()
				for _, width := range []int{80, 40} {
					model.Update(tea.WindowSizeMsg{Width: width, Height: 80})
					view := model.View()
					if noColor {
						if strings.Contains(view, "\x1b") || !strings.Contains(strings.Join(strings.Fields(view), ""), target) {
							t.Fatal("plain output lost the full Admin address or contains terminal controls")
						}
					} else {
						if !strings.Contains(view, ansi.SetHyperlink(target)) || !strings.Contains(view, ansi.ResetHyperlink()) {
							t.Fatal("terminal wrapping altered the full Admin hyperlink target")
						}
						plain := ansi.Strip(view)
						if !strings.Contains(plain, "http://localhost:4275/") || strings.Contains(plain, "#/connect") || strings.Contains(plain, "username=commander") {
							t.Fatal("Admin hyperlink label did not show the short browser address")
						}
					}
				}
			}
			setup := ui.New(ctx, ui.Config{AdminURL: adminURL, NoColor: noColor}, io.Discard)
			defer setup.Close()
			setup.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/admin")})
			setup.Update(tea.KeyMsg{Type: tea.KeyEnter})
			check(setup, adminURL)
			u, publish, _ := adminOperationUI(t, ctx, adminURL, noColor)
			defer u.model.Close()
			for _, command := range []string{"/admin", "/status"} {
				u.text(command)
				u.key(tea.KeyMsg{Type: tea.KeyEnter})
			}
			check(u.model, adminURL)
			publish(bridge.Operation{ID: "done", Status: "completed", UpdatedAt: time.Now()})
			u.text("/watch done")
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			u.wait("completion link", func(view string) bool { return strings.Contains(ansi.Strip(view), "Review on Admin UI") })
			check(u.model, "http://localhost:4275/#/connect?api=http%3A%2F%2F127.0.0.1%3A4274&operation=done&username=commander")
		})
	}
}

func TestOperationAdminLinkDestinations(t *testing.T) {
	const operationID = "result/part?key=%value#detail"
	for _, test := range []struct{ name, adminURL, target string }{
		{
			"remote Admin with a base path",
			"https://admin.example.test/commander",
			"https://admin.example.test/commander/#/operations/result%2Fpart%3Fkey=%25value%23detail",
		},
		{
			"local prefill with encoded values",
			"http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274%2Fapi&username=operator%2Blocal",
			"http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274%2Fapi&operation=result%2Fpart%3Fkey%3D%25value%23detail&username=operator%2Blocal",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			u, publish, _ := adminOperationUI(t, ctx, test.adminURL, false)
			defer u.model.Close()
			publish(bridge.Operation{ID: operationID, Status: "completed", UpdatedAt: time.Now()})
			u.text("/watch " + operationID)
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			u.wait("operation-specific review link", func(view string) bool {
				return strings.Contains(view, ansi.SetHyperlink(test.target))
			})
			for _, width := range []int{80, 40} {
				u.model.Update(tea.WindowSizeMsg{Width: width, Height: 50})
				view := u.model.View()
				if !strings.Contains(view, ansi.SetHyperlink(test.target)) {
					t.Fatal("wrapping changed the operation link destination")
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > width {
						t.Fatal("operation review link overflows the terminal width")
					}
				}
				if strings.Count(view, "\n") > 50 {
					t.Fatal("wrapped operation rows push the input outside the terminal")
				}
			}
		})
	}
}

func TestOperationProgressKeepsRecentLinksWithinTerminal(t *testing.T) {
	const adminURL = "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&username=commander"
	for _, noColor := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-color=%t", noColor), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			u, publish, _ := adminOperationUI(t, ctx, adminURL, noColor)
			defer u.model.Close()
			u.model.Update(tea.WindowSizeMsg{Width: 300, Height: 100})
			now := time.Now()
			for i, id := range []string{"alpha", "bravo", "charlie", "delta", "zulu"} {
				// The last two share a timestamp so their order also exercises the ID tie-break.
				publish(bridge.Operation{ID: id, Status: "completed", UpdatedAt: now.Add(time.Duration(min(i, 3)) * time.Second)})
				u.text("/watch " + id)
				u.key(tea.KeyMsg{Type: tea.KeyEnter})
				u.wait("completed "+id, func(view string) bool {
					return strings.Contains(ansi.Strip(view), id+" · completed") && !strings.Contains(view, "Thinking")
				})
			}
			view := ansi.Strip(u.model.View())
			previous := -1
			for _, id := range []string{"delta", "zulu", "charlie", "bravo"} {
				position := strings.Index(view, id+" · completed")
				if position <= previous {
					t.Fatal("progress does not show the most recent operations first with a stable ID tie-break")
				}
				previous = position
			}
			if strings.Contains(view, "alpha · completed") || !strings.Contains(view, "+1 more · /operations") {
				t.Fatal("progress did not keep its four-operation limit and overflow count")
			}
			const draft = "Keep this draft"
			u.text(draft)
			for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 20}, {Width: 80, Height: 24}} {
				u.model.Update(size)
				view := u.model.View()
				plain := ansi.Strip(view)
				if strings.Count(view, "\n") > size.Height {
					t.Fatalf("operation progress pushes input beyond a %dx%d terminal", size.Width, size.Height)
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > size.Width {
						t.Fatal("operation progress exceeds the terminal width")
					}
				}
				if !strings.Contains(plain, "delta · completed") || !strings.Contains(plain, draft) || !strings.Contains(plain, "/help · /exit") {
					t.Fatal("resizing hid the newest operation, input or footer")
				}
				displayed := strings.Count(strings.Join(strings.Fields(plain), " "), "Review on Admin UI")
				if displayed == 0 || displayed >= 5 || !strings.Contains(plain, fmt.Sprintf("+%d more · /operations", 5-displayed)) {
					t.Fatal("progress does not account for the operations hidden by its height budget")
				}
				for _, id := range []string{"delta", "zulu", "charlie", "bravo"} {
					if !strings.Contains(plain, id+" · completed") {
						continue
					}
					target := "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&operation=" + id + "&username=commander"
					if noColor {
						if !strings.Contains(strings.Join(strings.Fields(view), ""), target) {
							t.Fatal("height budgeting truncated a displayed operation URL")
						}
					} else if !strings.Contains(view, ansi.SetHyperlink(target)) {
						t.Fatal("height budgeting truncated a displayed operation hyperlink")
					}
				}
			}
		})
	}
}

func adminOperationUI(t *testing.T, ctx context.Context, adminURL string, noColor bool) (*uiDriver, func(bridge.Operation), *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	operations := make(map[string]string)
	publish := func(operation bridge.Operation) {
		t.Helper()
		encoded, err := json.Marshal(operation)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		operations[operation.ID] = string(encoded)
		mu.Unlock()
	}
	reads := &atomic.Int32{}
	server := mcp.NewServer(&mcp.Implementation{Name: "admin-operation-fixture", Version: "1"}, nil)
	for _, name := range []string{"get_operation", "operation_events"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			payload := `{"events":[],"next_cursor":""}`
			if request.Params.Name == "get_operation" {
				var args struct {
					ID string `json:"operation_id"`
				}
				if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
					return nil, err
				}
				mu.Lock()
				payload = operations[args.ID]
				mu.Unlock()
				reads.Add(1)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: payload}}}, nil
		})
	}
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(endpoint.Close)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error("could not inspect model request")
		}
		if strings.Contains(string(body), "Review on Admin UI") || strings.Contains(string(body), "http://localhost:4275") {
			t.Error("Admin link reached model history")
		}
		writeModelDelta(w, map[string]any{"content": "Operation reviewed"}, "stop")
	}))
	t.Cleanup(provider.Close)
	u := newUIWithConfig(t, ctx, ui.Config{
		Provider: "other", Model: "fixture", BaseURL: provider.URL, MCP: ui.Connection[host.BearerCredentials]{URL: endpoint.URL},
		Keys: map[string]string{"other": "test-only"}, NoColor: noColor, AdminURL: adminURL,
	})
	return u, publish, reads
}
