package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	databaseprovider "commander/providers/controller"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type operationSummaryView struct {
	ID, Status       string
	Mission          struct{ Name string }
	Input            map[string]any
	TaskCounts       map[string]int `json:"task_counts"`
	PendingDecisions int            `json:"pending_decisions"`
	PendingQuestions int            `json:"pending_questions"`
	ReportID         string         `json:"report_id"`
}

func TestMCPCompactCatalogsPreserveActionDetails(t *testing.T) {
	// Arrange: an explicit empty executable search scope keeps browser actions
	// unavailable even when CHROMIUM_PATH is configured. No actions execute.
	d := newDeployment(t)
	d.env["CHROMIUM_PATH"] = os.Getenv("CHROMIUM_PATH")
	wantStatus := map[string]actions.Status{
		"discover-pages":      actions.Ready,
		"check-links":         actions.Ready,
		"check-accessibility": actions.Unavailable,
		"capture-page":        actions.Unavailable,
		"check-seo":           actions.Ready,
		"detect-technologies": actions.Ready,
	}
	for range 2 {
		d.start(t, agentBinary, d.env, "--permissions=network-access,execute-command", "--search-paths=", "--heartbeat-interval=100ms")
	}
	endpoint, _ := d.mcp(t, false)
	session := connect(t, d.ctx, endpoint)
	readyAgents(t, d, session, 2)

	// Act: discover compact listings, then fetch each selected definition.
	listed := call(t, d.ctx, session, "list_agents", map[string]any{})
	inventory := decode[struct{ Agents []agentmodel.Agent }](t, listed)
	fullAgents := make([]agentmodel.Agent, 0, len(inventory.Agents))
	for _, agent := range inventory.Agents {
		full := decode[agentmodel.Agent](t, call(t, d.ctx, session, "describe_agent", map[string]any{"agent_id": agent.ID}))
		fullAgents = append(fullAgents, full)

		// Assert: identity, host, lease and grants remain available for selection.
		if agent.Status != full.Status || agent.Type != full.Type || agent.Stale || agent.LastHeartbeat.IsZero() || agent.Server != full.Server ||
			!reflect.DeepEqual(agent.GrantedPermissions, full.GrantedPermissions) || len(agent.Actions) != len(full.Actions) {
			t.Fatalf("agent summary lost selection information: %+v", agent)
		}
		if len(agent.Actions) != len(wantStatus) {
			t.Fatalf("expected all website actions: %+v", agent.Actions)
		}
		for i, action := range agent.Actions {
			original := full.Actions[i]
			if action.Name != original.Name || action.Status != original.Status || action.StatusReason != original.StatusReason ||
				!reflect.DeepEqual(action.RequiredPermissions, original.RequiredPermissions) || len(action.InputSchema) != 0 || len(action.OutputSchema) != 0 {
				t.Fatalf("action summary lost readiness or repeated schemas: %+v", action)
			}
			definition := decode[actions.Action](t, call(t, d.ctx, session, "describe_action", map[string]any{"agent_id": agent.ID, "action_name": action.Name}))
			if !reflect.DeepEqual(definition, original) {
				t.Fatalf("describe_action did not preserve the agent's full definition: %s", action.Name)
			}
			status, exists := wantStatus[action.Name]
			if !exists || action.Status != status {
				t.Fatalf("summary lost expected action readiness: %+v", action)
			}
			if status == actions.Unavailable && action.StatusReason == "" {
				t.Fatalf("missing Chromium reason was hidden from the summary: %+v", action)
			}
			if status == actions.Ready && action.StatusReason != "" {
				t.Fatalf("compiled-in action has an unexpected availability reason: %+v", action)
			}
		}
	}
	listedActions := call(t, d.ctx, session, "list_actions", map[string]any{})
	catalog := decode[struct{ Actions []actions.Action }](t, listedActions)
	fullActions := make([]actions.Action, 0, len(catalog.Actions))
	for _, action := range catalog.Actions {
		definition := decode[actions.Action](t, call(t, d.ctx, session, "describe_action", map[string]any{"action_name": action.Name}))
		fullActions = append(fullActions, definition)
		if action.Description != definition.Description || !reflect.DeepEqual(action.RequiredPermissions, definition.RequiredPermissions) ||
			len(action.InputSchema) != 0 || len(action.OutputSchema) != 0 || len(definition.InputSchema) == 0 || len(definition.OutputSchema) == 0 {
			t.Fatalf("catalog/detail lookup lost information: %s", action.Name)
		}
	}
	if len(catalog.Actions) != len(fullAgents[0].Actions) {
		t.Fatal("catalog listing omitted actions")
	}
	for _, args := range []map[string]any{
		{"action_name": "missing"}, {"action_name": " "},
		{"action_name": "check-links", "agent_id": "missing-agent"},
		{"action_name": "missing", "agent_id": inventory.Agents[0].ID},
	} {
		rejected(t, d.ctx, session, "describe_action", args)
	}

	// Compare the actual MCP response bodies with their former full-record shapes.
	assertSmallerListing(t, "list_agents", listed, map[string]any{"agents": fullAgents})
	assertSmallerListing(t, "list_actions", listedActions, map[string]any{"actions": fullActions})
}

func TestMCPOperationSummaryPreservesProgressAndDetail(t *testing.T) {
	// Arrange: saved work includes evidence, an interrupted task, model/code/user
	// continuations and both answered and pending questions. No controller runs.
	d := newDeployment(t)
	database, err := databaseprovider.OpenDatabase(d.ctx, d.env["DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	evidence, _ := json.Marshal(map[string]string{"observation": strings.Repeat("saved evidence ", 500)})
	operation := operations.Operation{
		ID: id.New(), Version: 1, Status: operations.Waiting, CreatedAt: now, UpdatedAt: now,
		Mission: missions.Mission{Name: "review", Objective: "Inspect evidence", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)},
		Input:   json.RawMessage(`{"target":"example.test"}`),
		Tasks: []operations.Task{
			{ID: id.New(), Status: operations.TaskSucceeded, Result: &actions.ActionOutput{Output: evidence}, Continuation: &operations.Continuation{Version: 3, Status: operations.ContinuationPending, DecisionBy: operations.DecisionModel}},
			{ID: id.New(), Status: operations.TaskInterrupted, Error: "outcome unknown"},
			{ID: id.New(), Status: operations.TaskQueued, Continuation: &operations.Continuation{Status: operations.ContinuationPending, DecisionBy: operations.DecisionCode}},
			{ID: id.New(), Status: operations.TaskSucceeded, Continuation: &operations.Continuation{Status: operations.ContinuationWaiting, DecisionBy: operations.DecisionUser}},
		},
		Questions: []operations.Question{
			{ID: id.New(), Status: operations.QuestionPending, Text: "Continue?", AskedAt: now},
			{ID: id.New(), Status: operations.QuestionAnswered, Text: "Target?", Answer: "example.test", AskedAt: now, AnsweredAt: &now},
		},
		Events: []operations.Event{{Sequence: 1, Type: operations.QuestionAsked, CreatedAt: now}},
	}
	if err := database.OperationStore.Create(d.ctx, operation); err != nil {
		t.Fatal(err)
	}
	endpoint, _ := d.mcpOnly(t, false)
	session := connect(t, d.ctx, endpoint)
	before := call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": operation.ID})

	// Act: list summaries, then retrieve the operation's full saved evidence.
	listed := call(t, d.ctx, session, "list_operations", map[string]any{})
	list := decode[struct{ Operations []operationSummaryView }](t, listed)
	after := call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": operation.ID})

	// Assert: progress exposes unfinished decisions without repeating evidence;
	// the detail query still provides all tasks, questions, schemas and events.
	if len(list.Operations) != 1 {
		t.Fatalf("operation missing from summary: %+v", list)
	}
	summary := list.Operations[0]
	if summary.ID != operation.ID || summary.Mission.Name != "review" || summary.Status != "waiting" || summary.Input["target"] != "example.test" ||
		summary.PendingDecisions != 1 || summary.PendingQuestions != 1 || !reflect.DeepEqual(summary.TaskCounts, map[string]int{"succeeded": 2, "interrupted": 1, "queued": 1}) {
		t.Fatalf("summary lost operation progress: %+v", summary)
	}
	if !reflect.DeepEqual(before.StructuredContent, after.StructuredContent) {
		t.Fatal("listing changed detailed operation state")
	}
	full := decode[operations.Operation](t, after)
	if len(full.Tasks) != 4 || full.Tasks[0].Continuation.Version != 3 || len(full.Questions) != 2 || len(full.Events) != 1 || !bytes.Equal(full.Tasks[0].Result.Output, evidence) {
		t.Fatal("detail query lost evidence or decisions")
	}
	assertSmallerListing(t, "list_operations", listed, map[string]any{"operations": []operations.Operation{full}})
}

func assertSmallerListing(t *testing.T, name string, result *mcp.CallToolResult, full any) {
	t.Helper()
	compact, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	original, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	// Leave room for descriptions/metadata to grow without fixing exact sizes.
	if len(compact) >= len(original)/2 {
		t.Fatalf("%s still repeats bulk detail: %d vs %d bytes", name, len(compact), len(original))
	}
	t.Logf("%s: %d -> %d JSON bytes (%.0f%% smaller)", name, len(original), len(compact), 100*(1-float64(len(compact))/float64(len(original))))
}
