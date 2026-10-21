package playground_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/controllers/default/data"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This exercises Playground's question and MCP handoff with scripted tool
// evidence. It does not execute a GitHub release, controller or spawned process.
func TestBuildReleaseInstallationHandoff(t *testing.T) {
	for _, answer := range []string{"Install", "Finish"} {
		t.Run(answer, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var definitions []missions.Mission
			if err := json.Unmarshal(data.DefaultMissions, &definitions); err != nil {
				t.Fatal(err)
			}
			catalog := map[string]missions.Mission{}
			for _, definition := range definitions {
				catalog[definition.Name] = definition
			}
			buildID, installID, releaseTaskID, spawnTaskID, questionID := id.New(), id.New(), id.TaskID(), id.TaskID(), id.New()
			const packageRef = "github.com/fixture/built-agent@v0.3.0"
			packageInput := map[string]any{"package": packageRef, "visibility": "private"}
			released := map[string]any{"repository": "fixture/built-agent", "commit_sha": strings.Repeat("a", 40), "version": "v0.3.0", "agent_type": "built-agent", "visibility": "private", "status": "released", "package": packageRef, "release_url": "https://github.com/fixture/built-agent/releases/tag/v0.3.0"}
			releaseJSON, _ := json.Marshal(released)
			buildOutput := map[string]any{"workspace_id": "saved-workspace", "summary": "Built and released", "release": released}
			now := time.Now()
			build := &operations.Operation{ID: buildID, Mission: catalog["build-agent"], Input: json.RawMessage(`{"instructions":"Build an agent"}`), Status: operations.Planning, UpdatedAt: now,
				Tasks: []operations.Task{{ID: releaseTaskID, OperationID: buildID, ActionName: "release-agent", Status: operations.TaskSucceeded, Result: &actions.ActionOutput{ActionName: "release-agent", Output: releaseJSON}, Continuation: &operations.Continuation{Version: 1, DecisionBy: operations.DecisionModel, Status: operations.ContinuationPending}}}}
			ops := map[string]*operations.Operation{buildID: build}
			var mu sync.Mutex
			stage, starts, spawns, readinessReads := 0, 0, 0, 0
			snapshot := func(op *operations.Operation) any {
				progress := op.AssessProgress(time.Now())
				return struct {
					*operations.Operation
					Progress bridge.Progress `json:"progress"`
				}{op, bridge.Progress{TaskCounts: map[string]int{}, TotalTasks: progress.TotalTasks, FinishedTasks: progress.FinishedTasks, PendingDecisions: progress.PendingDecisions, PendingQuestions: progress.PendingQuestions, Settled: progress.Settled}}
			}
			server := mcp.NewServer(&mcp.Implementation{Name: "build-install-fixture", Version: "1"}, nil)
			for _, name := range []string{"describe_mission", "describe_action", "get_operation", "operation_events", "ask_question", "answer_question", "resolve_continuation", "complete_operation", "start_operation", "assign_task", "list_agents"} {
				server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					mu.Lock()
					defer mu.Unlock()
					var args struct {
						OperationID  string `json:"operation_id"`
						MissionName  string `json:"mission_name"`
						ActionName   string `json:"action_name"`
						TaskID       string `json:"task_id"`
						QuestionID   string `json:"question_id"`
						Version      uint64 `json:"version"`
						Text, Answer string
						Options      []string
						Actions      []actions.NextAction
						Input        map[string]any
						Output       json.RawMessage
					}
					if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
						return nil, err
					}
					op := ops[args.OperationID]
					var payload any = map[string]any{}
					var err error
					switch req.Params.Name {
					case "describe_mission":
						payload = catalog[args.MissionName]
					case "describe_action":
						payload = map[string]any{"name": "spawn-agent", "status": "ready"}
					case "get_operation":
						payload = snapshot(op)
					case "operation_events":
						payload = map[string]any{"events": []any{}, "next_cursor": ""}
					case "ask_question":
						if args.OperationID != buildID || args.TaskID != releaseTaskID || args.Text != "Install this agent?" || !reflect.DeepEqual(args.Options, []string{"Install", "Finish"}) {
							t.Error("installation question did not link to the reviewed release")
						}
						err = op.AskQuestion(operations.Question{ID: questionID, TaskID: args.TaskID, Status: operations.QuestionPending, Text: args.Text, Options: args.Options, AskedAt: now, ExpiresAt: now.Add(time.Minute)}, args.Version)
						payload = snapshot(op)
					case "answer_question":
						if args.Answer != answer {
							t.Errorf("wrong operator answer: %q", args.Answer)
						}
						err = op.AnswerQuestion(args.QuestionID, args.Answer)
						payload = snapshot(op)
					case "resolve_continuation":
						if len(args.Actions) != 0 {
							t.Error("installation was queued inside an existing continuation")
						}
						err = op.ResolveContinuation(args.TaskID, args.Version, args.Actions)
					case "complete_operation":
						if op.ID == installID && readinessReads == 0 {
							t.Error("installation completed without checking action readiness")
						}
						err = op.Complete(id.New(), args.Output)
						payload = snapshot(op)
					case "start_operation":
						starts++
						if len(build.Questions) != 1 || build.Questions[0].Answer != "Install" || build.Status != operations.Completed || args.OperationID != installID || args.MissionName != "install-agent" || !reflect.DeepEqual(args.Input, packageInput) {
							t.Error("installation started without consent, completed build or exact verified package and visibility")
						}
						input, _ := json.Marshal(args.Input)
						ops[installID] = &operations.Operation{ID: installID, Mission: catalog["install-agent"], Input: input, Status: operations.Planning, UpdatedAt: time.Now()}
						payload = snapshot(ops[installID])
					case "assign_task":
						spawns++
						if args.OperationID != installID || args.ActionName != "spawn-agent" || !reflect.DeepEqual(args.Input, packageInput) {
							t.Error("spawn assignment changed the verified package or build-operation boundary")
						}
						op.Tasks = []operations.Task{{ID: spawnTaskID, OperationID: installID, ActionName: "spawn-agent", Status: operations.TaskSucceeded, Result: &actions.ActionOutput{ActionName: "spawn-agent", Output: json.RawMessage(`{"agent_id":"built-agent-12345678"}`)}, Continuation: &operations.Continuation{Version: 1, DecisionBy: operations.DecisionModel, Status: operations.ContinuationPending}}}
						payload = snapshot(op)
					case "list_agents":
						readinessReads++
						payload = map[string]any{"agents": []any{map[string]any{"id": "built-agent-12345678", "actions": []any{map[string]any{"name": "fixture-action", "status": "unavailable", "status_reason": "Configure its service before use"}}}}}
					}
					if err != nil {
						t.Errorf("%s: %v", req.Params.Name, err)
						return nil, err
					}
					encoded, _ := json.Marshal(payload)
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
				})
			}
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
			defer endpoint.Close()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				var body struct {
					Messages []struct{ Role, Name, Content string }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
					t.Error("missing model evidence")
					return
				}
				last := body.Messages[len(body.Messages)-1]
				if answer == "Finish" && stage >= 5 {
					writeModelDelta(w, map[string]any{"content": "Build handoff complete."}, "stop")
					return
				}
				var tool string
				var input map[string]any
				switch stage {
				case 0:
					if !strings.Contains(last.Content, packageRef) || !strings.Contains(last.Content, `"status":"released"`) {
						t.Error("saved release evidence did not reach model review")
					}
					tool, input = "describe_mission", map[string]any{"mission_name": "build-agent"}
				case 1:
					var received missions.Mission
					if json.Unmarshal([]byte(last.Content), &received) != nil || received.Objective != catalog["build-agent"].Objective {
						t.Error("model did not receive the bundled build mission")
					}
					tool, input = "ask_question", map[string]any{"operation_id": buildID, "task_id": releaseTaskID, "version": 1, "text": "Install this agent?", "options": []string{"Install", "Finish"}}
				case 2:
					writeModelDelta(w, map[string]any{"content": "Choose installation or finish with the released package."}, "stop")
					stage++
					return
				case 3:
					if !strings.Contains(last.Content, `"answer":"`+answer+`"`) || len(build.Questions) != 1 || build.Questions[0].Status != operations.QuestionAnswered {
						t.Error("model continued without the real operator answer")
					}
					tool, input = "resolve_continuation", map[string]any{"operation_id": buildID, "task_id": releaseTaskID, "version": 3, "actions": []any{}}
				case 4:
					tool, input = "complete_operation", map[string]any{"operation_id": buildID, "output": buildOutput}
				case 5:
					tool, input = "describe_mission", map[string]any{"mission_name": "install-agent"}
				case 6:
					tool, input = "describe_action", map[string]any{"action_name": "spawn-agent"}
				case 7:
					tool, input = "start_operation", map[string]any{"operation_id": installID, "mission_name": "install-agent", "input": packageInput}
				case 8:
					tool, input = "assign_task", map[string]any{"operation_id": installID, "action_name": "spawn-agent", "input": packageInput}
				case 9:
					tool, input = "get_operation", map[string]any{"operation_id": installID}
				case 10:
					if !strings.Contains(last.Content, "built-agent-12345678") {
						t.Error("saved spawn result missing")
					}
					tool, input = "resolve_continuation", map[string]any{"operation_id": installID, "task_id": spawnTaskID, "version": 1, "actions": []any{}}
				case 11:
					tool, input = "list_agents", map[string]any{}
				case 12:
					if !strings.Contains(last.Content, "unavailable") {
						t.Error("model did not receive action readiness")
					}
					tool, input = "complete_operation", map[string]any{"operation_id": installID, "output": map[string]any{"agent_id": "built-agent-12345678", "limitations": []string{"Configure its service before use"}}}
				}
				stage++
				if tool == "" {
					writeModelDelta(w, map[string]any{"content": "Build handoff complete."}, "stop")
				} else {
					writeModelDelta(w, toolDelta(tool, input), "tool_calls")
				}
			}))
			defer provider.Close()
			u := newUI(t, ctx, endpoint.URL, provider.URL)
			defer u.model.Close()
			u.text("/watch " + buildID)
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			u.wait("installation approval", func(view string) bool {
				return strings.Contains(view, "Install this agent?") && strings.Contains(view, "Choose installation") && !strings.Contains(view, "Thinking")
			})
			mu.Lock()
			if starts != 0 || spawns != 0 || build.Status == operations.Completed {
				t.Error("installation or completion ran before operator answer")
			}
			mu.Unlock()
			if answer == "Finish" {
				u.key(tea.KeyMsg{Type: tea.KeyDown})
			}
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			u.wait("handoff completion", func(view string) bool {
				return strings.Contains(view, "Build handoff complete.") && !strings.Contains(view, "Thinking")
			})
			mu.Lock()
			defer mu.Unlock()
			if build.Status != operations.Completed || !strings.Contains(string(build.Output), packageRef) {
				t.Error("build lost its successful release report")
			}
			want := 0
			if answer == "Install" {
				want = 1
			}
			if starts != want || spawns != want {
				t.Errorf("install operations=%d, spawn assignments=%d; want %d", starts, spawns, want)
			}
			if want == 1 && (ops[installID].Status != operations.Completed || !strings.Contains(string(ops[installID].Output), "Configure its service")) {
				t.Error("installation lost its readiness limitation")
			}
		})
	}
}
