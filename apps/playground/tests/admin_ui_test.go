package playground_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
	"github.com/defos-ai/commander/framework/host"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAdminCommandDuringSetup(t *testing.T) {
	for _, test := range []struct {
		name, url, hint, want string
	}{
		{"configured", "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&username=commander", "", "Admin: http://localhost:4275/"},
		{"unavailable", "", "Admin UI could not start. Try /admin serve.", "Admin UI could not start. Try /admin serve."},
		{"unconfigured", "", "", "Start Admin with /admin serve."},
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
			if !strings.Contains(model.View(), "/admin [command] — Admin UI and commands") {
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
	const missionName, missionObjective = "local-admin-mission", "Local CLI mission objective"
	var toolCalls, modelCalls, apiCalls, serveCalls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		username, actualPassword, ok := r.BasicAuth()
		if !ok || username != "commander" || actualPassword != password {
			t.Error("Admin command did not use Playground's API credentials")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet || (r.URL.Path != "/api/v1/missions" && r.URL.Path != "/api/v1/sessions") {
			t.Errorf("unexpected Admin API request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/sessions" {
			fmt.Fprint(w, `{"items":[]}`)
			return
		}
		fmt.Fprintf(w, `{"items":[{"name":%q,"objectives":[%q],"required_actions":["review"],"starting_actions":["review"],"input_schema":{"type":"object"},"output_schema":{}}]}`, missionName, missionObjective)
	}))
	defer api.Close()
	started, release := make(chan struct{}), make(chan struct{})
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
		for _, private := range []string{adminURL, api.URL, "Usage: /admin", "Usage: commander-admin", missionName, missionObjective, "No sessions.", password, token, "--listen-address=127.0.0.1:0"} {
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
		fakes.WriteModelDelta(w, map[string]any{"content": "Normal chat works"}, "stop")
	}))
	defer provider.Close()
	u := fixtures.NewUIWithConfig(t, ctx, ui.Config{
		Provider: "other", Model: "fixture", BaseURL: provider.URL, MCP: ui.Connection[host.BearerCredentials]{URL: endpoint.URL, Credentials: host.BearerCredentials{Token: token}},
		NoColor: true, AdminURL: adminURL,
		LocalAgentCount: 4,
		AllInOne:        true,
		API:             ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: password}},
		ServeAdmin: func(ctx context.Context, args []string) (string, error) {
			if serveCalls.Add(1) == 1 {
				close(started)
			}
			if len(args) != 1 || args[0] != "--listen-address=127.0.0.1:0" {
				t.Error("Admin serve did not pass local options to its host")
			}
			select {
			case <-release:
				return "http://localhost:43275", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	})
	defer u.Close()
	u.Model.Update(tea.WindowSizeMsg{Width: 180, Height: 80})
	if strings.Contains(u.Model.View(), "Admin:") || strings.Contains(u.Model.View(), adminURL) {
		t.Fatal("Admin link added noise to startup")
	}
	for _, command := range []string{"/admin unexpected", "/admin", "/admin", "/help"} {
		u.Text(command)
		u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	}
	u.Wait("help including Admin", func(view string) bool {
		return strings.Contains(view, "/admin [command] — Admin UI and commands")
	})
	view := u.Model.View()
	if !strings.Contains(view, "Usage: /admin") || strings.Count(view, "Admin: "+adminURL) != 2 {
		t.Fatal("Admin command is not repeatable or did not preserve its link")
	}
	if modelCalls.Load() != 0 || toolCalls.Load() != 0 {
		t.Fatal("Admin command or help called the model or MCP tools")
	}
	u.Text("/admin help")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("local Admin CLI help", func(view string) bool { return strings.Contains(view, "Usage: commander-admin") })
	u.Text("/admin missions list --json")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("local mission JSON", func(view string) bool { return strings.Contains(view, missionObjective) })
	if apiCalls.Load() != 1 {
		t.Fatal("Admin mission command did not make exactly one API request")
	}
	u.Text("/admin sessions list")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("local session discovery", func(view string) bool { return strings.Contains(view, "No sessions.") })
	if apiCalls.Load() != 2 || modelCalls.Load() != 0 || toolCalls.Load() != 0 {
		t.Fatal("Admin session command did not use only the configured API")
	}
	u.Text("/admin serve --listen-address=127.0.0.1:0")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("Admin serve callback did not start")
	}
	u.Text("/admin serve --listen-address=127.0.0.1:0")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(u.Model.View(), "An Admin command is running") || serveCalls.Load() != 1 {
		t.Fatal("concurrent Admin command was not rejected")
	}
	close(release)
	wantURL := "http://localhost:43275/#/connect?api=" + url.QueryEscape(api.URL) + "&username=commander"
	u.Wait("ready Admin link", func(view string) bool { return strings.Contains(view, "Admin: "+wantURL) })
	u.Text("/status")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(u.Model.View(), wantURL) || serveCalls.Load() != 1 || modelCalls.Load() != 0 || toolCalls.Load() != 0 {
		t.Fatal("Admin result did not update its link locally")
	}
	view = u.Model.View()
	for _, private := range []string{password, token} {
		if strings.Contains(view, private) {
			t.Fatal("Admin command disclosed credentials")
		}
	}
	u.Text("Hello")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("chat after Admin command", func(view string) bool {
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
	u, operations := fixtures.AdminOperationUI(t, ctx, adminURL, true)
	defer u.Close()
	u.Model.Update(tea.WindowSizeMsg{Width: 300, Height: 100})
	command := func(text string) { u.Text(text); u.Key(tea.KeyMsg{Type: tea.KeyEnter}) }
	countLinks := func(want int) {
		t.Helper()
		if got := strings.Count(u.Model.View(), "Review on Admin UI ("); got != want {
			t.Fatalf("Admin review link appeared %d times, want %d", got, want)
		}
		if strings.Contains(u.Model.View(), "View results in Admin:") {
			t.Fatal("completion still adds a separate chat notice")
		}
	}
	command("/admin")
	countLinks(0)
	now := time.Now()
	operations.Publish(bridge.Operation{ID: "failed", Status: "failed", UpdatedAt: now, Progress: bridge.Progress{TaskCounts: map[string]int{}, Settled: true}})
	command("/watch failed")
	u.Wait("failed operation reviewed", func(view string) bool {
		return strings.Contains(view, "failed · failed") && strings.Count(view, "Operation reviewed") == 1 && !strings.Contains(view, "Thinking")
	})
	countLinks(0)
	cancelled := bridge.Operation{
		ID: "cancelled", Status: "cancelled", UpdatedAt: now,
		Tasks:    []bridge.Task{{ID: "known", Status: "succeeded"}, {ID: "unknown", Status: "interrupted"}},
		Progress: bridge.Progress{TaskCounts: map[string]int{"succeeded": 1, "interrupted": 1}, TotalTasks: 2, FinishedTasks: 1},
	}
	operations.Publish(cancelled)
	command("/watch cancelled")
	targetReads := operations.ReadCount() + 3
	u.Wait("cancelled work awaiting its outcome", func(view string) bool {
		return strings.Contains(view, "○ cancelle · cancelled · 1/2 tasks finished · 1 task outcome unknown") && operations.ReadCount() >= targetReads
	})
	if strings.Contains(u.Model.View(), "✓ cancelle") || strings.Count(u.Model.View(), "Operation reviewed") != 1 {
		t.Fatal("interrupted cancellation was marked settled or reviewed before its outcome arrived")
	}
	countLinks(0)
	cancelled.Tasks[1].Status = "failed"
	cancelled.UpdatedAt = now.Add(time.Second)
	cancelled.Progress = bridge.Progress{TaskCounts: map[string]int{"succeeded": 1, "failed": 1}, TotalTasks: 2, FinishedTasks: 2, Settled: true}
	operations.Publish(cancelled)
	u.Wait("late cancellation outcome reviewed once", func(view string) bool {
		return strings.Contains(view, "✓ cancelle · cancelled · 2/2 tasks finished") && strings.Count(view, "Operation reviewed") == 2 && !strings.Contains(view, "Thinking")
	})
	countLinks(0)
	op := bridge.Operation{ID: "target", Status: "running", UpdatedAt: now, Tasks: []bridge.Task{{ID: "task", Status: "succeeded"}}, Progress: bridge.Progress{TaskCounts: map[string]int{"succeeded": 1}, TotalTasks: 1, FinishedTasks: 1}}
	operations.Publish(op)
	command("/watch target")
	u.Wait("completed task in running operation", func(view string) bool { return strings.Contains(view, "target · running · 1/1 tasks finished") })
	countLinks(0)

	op.Status, op.Tasks[0].Status = "completed", "running"
	op.Progress = bridge.Progress{TaskCounts: map[string]int{"running": 1}, TotalTasks: 1}
	op.UpdatedAt = now.Add(-time.Second)
	operations.Publish(op)
	targetReads = operations.ReadCount() + 2
	u.Wait("stale snapshot read", func(string) bool { return operations.ReadCount() >= targetReads })
	op.Status, op.UpdatedAt = "waiting", now.Add(time.Second)
	operations.Publish(op)
	u.Wait("fresh snapshot after stale completion", func(view string) bool { return strings.Contains(view, "target · waiting") })
	countLinks(0)

	op.Status, op.UpdatedAt = "completed", now.Add(2*time.Second)
	op.Tasks[0].Status = "interrupted"
	op.Progress = bridge.Progress{TaskCounts: map[string]int{"interrupted": 1}, TotalTasks: 1}
	operations.Publish(op)
	u.Wait("completion with an unknown outcome", func(view string) bool {
		return strings.Contains(view, "○ target · completed · 0/1 tasks finished · 1 task outcome unknown")
	})
	countLinks(0)
	op.UpdatedAt = now.Add(3 * time.Second)
	operations.Publish(op)
	targetReads = operations.ReadCount() + 2
	u.Wait("repeated completion snapshots", func(string) bool { return operations.ReadCount() >= targetReads })
	if !strings.Contains(u.Model.View(), "○ target · completed · 0/1 tasks finished · 1 task outcome unknown") || strings.Count(u.Model.View(), "Operation reviewed") != 2 {
		t.Fatal("completed operation animated or triggered another model review while its outcome remained unknown")
	}
	op.Tasks[0].Status, op.UpdatedAt = "succeeded", now.Add(4*time.Second)
	op.Progress = bridge.Progress{TaskCounts: map[string]int{"succeeded": 1}, TotalTasks: 1, FinishedTasks: 1, Settled: true}
	operations.Publish(op)
	u.Wait("settled completion", func(view string) bool {
		return strings.Contains(view, "target · completed · 1/1 tasks finished") && strings.Count(view, "Operation reviewed") == 3 && !strings.Contains(view, "Thinking")
	})
	countLinks(1)
	if strings.Contains(u.Model.View(), "task outcome unknown") || strings.Contains(u.Model.View(), "task outcomes unknown") {
		t.Fatal("known late outcomes still appear as unknown")
	}
	const targetURL = "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&operation=target&username=commander"
	if !strings.Contains(u.Model.View(), "target · completed · 1/1 tasks finished · Review on Admin UI ("+targetURL+")") {
		t.Fatal("review link does not appear on its operation's progress row")
	}
	command("/clear")
	u.Wait("cleared model history", func(view string) bool { return strings.Contains(view, "Model conversation cleared") })
	operations.Publish(bridge.Operation{ID: "second", Status: "completed", UpdatedAt: now, Progress: bridge.Progress{TaskCounts: map[string]int{}, Settled: true}})
	command("/watch second")
	u.Wait("second completed operation", func(view string) bool { return strings.Contains(view, "second · completed") })
	countLinks(2)
	if !strings.Contains(u.Model.View(), "operation=second&username=commander") || !strings.Contains(u.Model.View(), "operation=target&username=commander") {
		t.Fatal("completed operations do not have distinct destinations")
	}
	command("/admin")
	if strings.Count(u.Model.View(), "Admin: "+adminURL) != 2 {
		t.Fatal("Admin command did not remain available after operation completion")
	}
}

func TestAdminCompletionWithoutURLIsSilent(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	u, operations := fixtures.AdminOperationUI(t, ctx, "", true)
	defer u.Close()
	operations.Publish(bridge.Operation{ID: "completed", Status: "completed", UpdatedAt: time.Now(), Progress: bridge.Progress{TaskCounts: map[string]int{}, Settled: true}})
	u.Text("/watch completed")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("completion without Admin", func(view string) bool { return strings.Contains(view, "complete · completed") })
	if strings.Contains(u.Model.View(), "Review on Admin UI") || strings.Contains(u.Model.View(), "--admin-url") {
		t.Fatal("completion advertised an unknown Admin address")
	}
	u.Text("/admin")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(u.Model.View(), "Start Admin with /admin serve.") {
		t.Fatal("explicit Admin lookup did not explain missing configuration")
	}
}

func TestFailedOperationShowsUnknownTaskOutcomes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	u, operations := fixtures.AdminOperationUI(t, ctx, "", true)
	defer u.Close()
	u.Model.Update(tea.WindowSizeMsg{Width: 180, Height: 60})
	op := bridge.Operation{
		ID: "failed", Status: "failed", UpdatedAt: time.Now(),
		Tasks:    []bridge.Task{{ID: "first", Status: "interrupted"}, {ID: "second", Status: "interrupted"}},
		Progress: bridge.Progress{TaskCounts: map[string]int{"interrupted": 2}, TotalTasks: 2},
	}
	operations.Publish(op)
	u.Text("/watch failed")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("failed operation with unknown outcomes", func(view string) bool {
		return strings.Contains(view, "○ failed · failed · 0/2 tasks finished · 2 task outcomes unknown") && operations.ReadCount() >= 3
	})
	if strings.Contains(u.Model.View(), "Operation reviewed") {
		t.Fatal("unknown outcomes triggered a final model review")
	}
	op.Tasks[0].Status, op.Tasks[1].Status = "failed", "failed"
	op.UpdatedAt = op.UpdatedAt.Add(time.Second)
	op.Progress = bridge.Progress{TaskCounts: map[string]int{"failed": 2}, TotalTasks: 2, FinishedTasks: 2, Settled: true}
	operations.Publish(op)
	u.Wait("failed operation with known outcomes", func(view string) bool {
		return strings.Contains(view, "✓ failed · failed · 2/2 tasks finished") && strings.Contains(view, "Operation reviewed") && !strings.Contains(view, "Thinking")
	})
	if strings.Contains(u.Model.View(), "task outcomes unknown") {
		t.Fatal("settled operation still shows unknown outcomes")
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
			u, operations := fixtures.AdminOperationUI(t, ctx, adminURL, noColor)
			defer u.Close()
			for _, command := range []string{"/admin", "/status"} {
				u.Text(command)
				u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			}
			check(u.Model, adminURL)
			operations.Publish(bridge.Operation{ID: "done", Status: "completed", UpdatedAt: time.Now(), Progress: bridge.Progress{TaskCounts: map[string]int{}, Settled: true}})
			u.Text("/watch done")
			u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			u.Wait("completion link", func(view string) bool { return strings.Contains(ansi.Strip(view), "Review on Admin UI") })
			check(u.Model, "http://localhost:4275/#/connect?api=http%3A%2F%2F127.0.0.1%3A4274&operation=done&username=commander")
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
			u, operations := fixtures.AdminOperationUI(t, ctx, test.adminURL, false)
			defer u.Close()
			operations.Publish(bridge.Operation{ID: operationID, Status: "completed", UpdatedAt: time.Now(), Progress: bridge.Progress{TaskCounts: map[string]int{}, Settled: true}})
			u.Text("/watch " + operationID)
			u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			u.Wait("operation-specific review link", func(view string) bool {
				return strings.Contains(view, ansi.SetHyperlink(test.target))
			})
			for _, width := range []int{80, 40} {
				u.Model.Update(tea.WindowSizeMsg{Width: width, Height: 50})
				view := u.Model.View()
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
			u, operations := fixtures.AdminOperationUI(t, ctx, adminURL, noColor)
			defer u.Close()
			u.Model.Update(tea.WindowSizeMsg{Width: 300, Height: 100})
			now := time.Now()
			for i, id := range []string{"alpha", "bravo", "charlie", "delta", "zulu"} {
				// The last two share a timestamp so their order also exercises the ID tie-break.
				operations.Publish(bridge.Operation{ID: id, Status: "completed", UpdatedAt: now.Add(time.Duration(min(i, 3)) * time.Second), Progress: bridge.Progress{TaskCounts: map[string]int{}, Settled: true}})
				u.Text("/watch " + id)
				u.Key(tea.KeyMsg{Type: tea.KeyEnter})
				u.Wait("completed "+id, func(view string) bool {
					return strings.Contains(ansi.Strip(view), id+" · completed") && !strings.Contains(view, "Thinking")
				})
			}
			view := ansi.Strip(u.Model.View())
			previous := -1
			for _, id := range []string{"delta", "zulu", "charlie"} {
				position := strings.Index(view, id+" · completed")
				if position <= previous {
					t.Fatal("progress does not show the most recent operations first with a stable ID tie-break")
				}
				previous = position
			}
			if strings.Contains(view, "alpha · completed") || strings.Contains(view, "bravo · completed") || !strings.Contains(view, "+2 more · /operations") {
				t.Fatal("progress did not keep its three-operation limit and overflow count")
			}
			if strings.Count(view, "\n") != 99 || !strings.HasSuffix(view, "/help · /exit\n") {
				t.Fatal("chat does not leave exactly one blank line below its footer")
			}
			const draft = "Keep this draft"
			u.Text(draft)
			for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 20}, {Width: 80, Height: 24}} {
				u.Model.Update(size)
				view := u.Model.View()
				plain := ansi.Strip(view)
				if strings.Count(view, "\n") != size.Height-1 || !strings.HasSuffix(plain, "/help · /exit\n") {
					t.Fatalf("chat does not fit a %dx%d terminal with one bottom margin line", size.Width, size.Height)
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
				if displayed == 0 || displayed > 3 || !strings.Contains(plain, fmt.Sprintf("+%d more · /operations", 5-displayed)) {
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
