package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPDiscoveryAndClientLifetime(t *testing.T) {
	// Arrange: two independent agents use environment connections; MCP flags
	// deliberately override invalid connection and logging environment values.
	d := newDeployment(t)
	first, second := d.agent(t), d.agent(t)
	endpoint, adapter := d.mcp(t, true)
	session := connect(t, d.ctx, endpoint)
	registered := readyAgents(t, d, session, 2)

	// Act: use two clients, disconnect both, and reconnect to the same server.
	another := connect(t, d.ctx, endpoint)
	otherInventory := agents(t, d, another)
	if len(otherInventory) != len(registered) {
		t.Fatalf("second client inventory: %+v", otherInventory)
	}
	if err := another.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	session = connect(t, d.ctx, endpoint)
	reconnected := readyAgents(t, d, session, 2)
	catalog := decode[struct {
		Actions []struct {
			Name        string
			InputSchema map[string]any `json:"input_schema"`
		}
	}](t, call(t, d.ctx, session, "list_actions", map[string]any{}))
	definition := decode[struct {
		Name, Status string
		InputSchema  map[string]any `json:"input_schema"`
	}](t, call(t, d.ctx, session, "describe_action", map[string]any{"action_name": "check-links"}))

	// Assert: client lifetime does not change agent identity or controller lifetime.
	if len(catalog.Actions) != 1 || catalog.Actions[0].Name != "check-links" ||
		catalog.Actions[0].InputSchema != nil || definition.Name != "check-links" ||
		definition.Status != "unknown" || definition.InputSchema["type"] != "object" {
		t.Fatalf("agent announcements did not populate a shared action catalog: %+v", catalog)
	}
	if adapter.stopped() || d.controllerProcess.stopped() || first.stopped() || second.stopped() {
		t.Fatal("disconnecting MCP clients stopped a server")
	}
	idPattern := regexp.MustCompile(`^web-quality-agent-[0-9a-f]{8}$`)
	for i, record := range registered {
		if !idPattern.MatchString(record.ID) || record.Type != "web-quality" {
			t.Fatalf("unexpected public agent identity: %+v", record)
		}
		if record.ID != otherInventory[i].ID || record.ID != reconnected[i].ID {
			t.Fatal("clients observed different agent identities")
		}
		if len(record.Actions) != 1 || record.Actions[0].Name != "check-links" {
			t.Fatalf("agent did not advertise configured action: %+v", record.Actions)
		}
	}
	if registered[0].ID == registered[1].ID {
		t.Fatal("agent identities are not independent")
	}

	// Act: stop one selected agent, then the remaining agent, through MCP.
	call(t, d.ctx, session, "stop_agent", map[string]any{"agent_id": registered[0].ID})
	poll(t, func() bool { return first.stopped() || second.stopped() })

	// Assert: a directed stop affects one process and leaves the other available.
	if first.stopped() == second.stopped() {
		t.Fatal("directed stop did not affect exactly one agent")
	}
	remaining := decode[agentView](t, call(t, d.ctx, session, "describe_agent", map[string]any{"agent_id": registered[1].ID}))
	if remaining.Status != "ready" || remaining.Stale {
		t.Fatalf("other agent became unavailable: %+v", remaining)
	}
	call(t, d.ctx, session, "stop_agent", map[string]any{"agent_id": registered[1].ID})
	first.wait(t)
	second.wait(t)
	adapter.interrupt(t)
}

func TestMCPActionExecutionAndRecovery(t *testing.T) {
	// Arrange: real actions access only a local HTTP fixture; results are stored.
	d := newDeployment(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "local evidence") }))
	defer server.Close()
	endpoint, _ := d.mcp(t, false)
	d.agent(t)
	session := connect(t, d.ctx, endpoint)
	agent := readyAgents(t, d, session, 1)[0]

	for _, scenario := range []struct {
		name, action string
		input        map[string]any
	}{
		{"invalid action input", "check-links", map[string]any{"url": "file:///invalid"}},
		{"unknown action", "missing-action", map[string]any{}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: each scenario owns its client connection.
			client := connect(t, d.ctx, endpoint)
			arguments := map[string]any{"agent_id": agent.ID, "action_name": scenario.action, "input": scenario.input}

			// Act.
			result, err := client.CallTool(d.ctx, &mcp.CallToolParams{Name: "run_action", Arguments: arguments})

			// Assert: application errors are returned through the MCP action result.
			if err != nil || !result.IsError {
				t.Fatalf("action failure response: %+v, %v", result, err)
			}

			// Act: perform valid work on the same client after the failure.
			result = call(t, d.ctx, client, "run_action", map[string]any{"agent_id": agent.ID, "action_name": "check-links", "input": map[string]any{"url": server.URL}})
			body := readLinkArtifact(t, d.ctx, client, result)

			// Assert: the agent recovered and its advertised artifact is readable.
			if body != server.URL {
				t.Fatalf("artifact content = %q", body)
			}
		})
	}

	t.Run("protocol errors leave the server available", func(t *testing.T) {
		// Arrange.
		client := connect(t, d.ctx, endpoint)
		// Act.
		result, err := client.CallTool(d.ctx, &mcp.CallToolParams{Name: "list_agents", Arguments: map[string]any{"unexpected": true}})
		// Assert.
		if err == nil && !result.IsError {
			t.Fatal("MCP accepted unexpected arguments")
		}

		// Act: HTTP protocol errors may close that SDK client, so reconnect.
		client = connect(t, d.ctx, endpoint)
		registered := readyAgents(t, d, client, 1)
		// Assert.
		if registered[0].ID != agent.ID {
			t.Fatal("protocol failure replaced the agent")
		}
	})

	for _, uri := range []string{"missions://artifacts/00000000-0000-4000-8000-000000000000", "missions://artifacts/../../etc/passwd"} {
		t.Run("reject resource "+uri, func(t *testing.T) {
			// Arrange: each request starts with a verified live connection.
			client := connect(t, d.ctx, endpoint)
			// Act.
			_, err := client.ReadResource(d.ctx, &mcp.ReadResourceParams{URI: uri})
			// Assert.
			if err == nil {
				t.Fatalf("read unreported resource %s", uri)
			}
		})
	}
}

// This input catalog is owned by the test, independent of developer state data.
const missionCatalog = `[{
 "name":"local-observation", "objective":"Read a local fixture and save a summary",
 "required_actions":[], "starting_actions":[],
 "input_schema":{"type":"object","additionalProperties":false},
 "output_schema":{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"],"additionalProperties":false}
}]`

type operationView struct {
	ID, Status string
	ReportID   string `json:"report_id"`
	Tasks      []struct{ ID, Status string }
}

type reportView struct {
	ID          string
	OperationID string `json:"operation_id"`
	Mission     string
	Output      struct{ Summary string }
	CreatedAt   string `json:"created_at"`
}

func TestMCPQueriesSurviveControllerStopAndAdapterRestart(t *testing.T) {
	// Arrange: seed only the deployment's input catalog, then start real processes.
	d := newDeployment(t)
	database := filepath.Join(d.root, "database")
	if err := os.MkdirAll(database, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(database, "missions.json"), []byte(missionCatalog), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "mission evidence") }))
	defer server.Close()
	endpoint, adapter := d.mcp(t, false)
	d.agent(t)
	session := connect(t, d.ctx, endpoint)
	agent := readyAgents(t, d, session, 1)[0]

	// Act: discover the mission, start it, and ask for a decision before work.
	catalog := decode[struct{ Missions []struct{ Name string } }](t, call(t, d.ctx, session, "list_missions", map[string]any{}))
	if !slices.ContainsFunc(catalog.Missions, func(m struct{ Name string }) bool { return m.Name == "local-observation" }) {
		t.Fatalf("mission catalog: %+v", catalog)
	}
	rejected(t, d.ctx, session, "start_operation", map[string]any{"mission_name": "local-observation", "input": map[string]any{"unexpected": true}})
	const operationID = "a98051f0-c651-4cdd-b8a1-f5158c20be67"
	for _, invalid := range []string{"not-a-uuid", "../operations", "a98051f0c6514cddb8a1f5158c20be67"} {
		rejected(t, d.ctx, session, "start_operation", map[string]any{"operation_id": invalid, "mission_name": "local-observation", "input": map[string]any{}})
	}
	start := map[string]any{"operation_id": operationID, "mission_name": "local-observation", "input": map[string]any{}}
	operation := decode[operationView](t, call(t, d.ctx, session, "start_operation", start))
	if operation.ID != operationID {
		t.Fatalf("MCP replaced the supplied operation ID: %s", operation.ID)
	}
	rejected(t, d.ctx, session, "start_operation", start)
	question := decode[struct{ ID string }](t, call(t, d.ctx, session, "ask_question", map[string]any{"operation_id": operation.ID, "text": "Read the local fixture?"}))
	assignment := map[string]any{"operation_id": operation.ID, "agent_id": agent.ID, "action_name": "check-links", "input": map[string]any{"url": server.URL}}
	rejected(t, d.ctx, session, "assign_task", assignment)
	call(t, d.ctx, session, "answer_question", map[string]any{"operation_id": operation.ID, "question_id": question.ID, "answer": "yes"})
	task := decode[struct{ ID string }](t, call(t, d.ctx, session, "assign_task", assignment))

	// Assert: the selected task finishes and its events are externally observable.
	poll(t, func() bool {
		current := decode[operationView](t, call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": operation.ID}))
		if len(current.Tasks) != 1 || current.Tasks[0].ID != task.ID {
			t.Fatalf("operation lost assigned task: %+v", current)
		}
		if current.Tasks[0].Status == "running" {
			return false
		}
		if current.Tasks[0].Status != "succeeded" {
			t.Fatalf("task failed: %+v", current)
		}
		return true
	})
	events := decode[struct{ Events []json.RawMessage }](t, call(t, d.ctx, session, "operation_events", map[string]any{"operation_id": operation.ID}))
	if len(events.Events) == 0 {
		t.Fatal("operation produced no events")
	}
	rejected(t, d.ctx, session, "complete_operation", map[string]any{"operation_id": operation.ID, "output": map[string]any{}})

	// Arrange: capture the completed report, operation, and history while the
	// controller is running. All later reads use these persisted results.
	completed := decode[operationView](t, call(t, d.ctx, session, "complete_operation", map[string]any{"operation_id": operation.ID, "output": map[string]any{"summary": "local fixture inspected"}}))
	before := decode[reportView](t, call(t, d.ctx, session, "get_report", map[string]any{"operation_id": operation.ID}))
	evidence := getMission(t, d, session, operation.ID).Tasks[0]
	history := decode[eventPage](t, call(t, d.ctx, session, "operation_events", map[string]any{"operation_id": operation.ID}))

	// Act: stop the controller, then read with both the existing MCP process
	// and a fresh MCP process. No controller is started during either phase.
	d.controllerProcess.interrupt(t)
	for _, restart := range []bool{false, true} {
		if restart {
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			adapter.interrupt(t)
			endpoint, _ = d.mcpOnly(t, false)
			session = connect(t, d.ctx, endpoint)
		}
		queryCtx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
		after := decode[reportView](t, call(t, queryCtx, session, "get_report", map[string]any{"operation_id": operation.ID}))
		current := decode[operationView](t, call(t, queryCtx, session, "get_operation", map[string]any{"operation_id": operation.ID}))
		listed := decode[struct{ Operations []operationSummaryView }](t, call(t, queryCtx, session, "list_operations", map[string]any{}))
		offlineCatalog := decode[struct{ Missions []struct{ Name string } }](t, call(t, queryCtx, session, "list_missions", map[string]any{}))
		actions := decode[struct{ Actions []struct{ Name string } }](t, call(t, queryCtx, session, "list_actions", map[string]any{}))
		updates := decode[eventPage](t, call(t, queryCtx, session, "operation_events", map[string]any{"operation_id": operation.ID}))
		end := decode[eventPage](t, call(t, queryCtx, session, "operation_events", map[string]any{"operation_id": operation.ID, "after_sequence": history.NextSequence, "wait_seconds": 30}))
		cancel()
		assertTaskEvidence(t, d, session, evidence, server.URL)

		// Assert: catalogs, snapshots, reports, events and accepted artifacts stay
		// readable offline; a terminal event cursor returns immediately.
		if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(completed, current) || len(listed.Operations) != 1 {
			t.Fatalf("offline query changed saved work: report=%+v, operation=%+v, list=%+v", after, current, listed)
		}
		summary := listed.Operations[0]
		if summary.ID != completed.ID || summary.Status != completed.Status || summary.ReportID != completed.ReportID || summary.TaskCounts["succeeded"] != 1 || summary.PendingQuestions != 0 || summary.PendingDecisions != 0 {
			t.Fatalf("offline summary lost saved progress: %+v", summary)
		}
		if !reflect.DeepEqual(offlineCatalog, catalog) || len(actions.Actions) != 1 || actions.Actions[0].Name != "check-links" {
			t.Fatalf("offline catalogs: missions=%+v actions=%+v", offlineCatalog, actions)
		}
		if !reflect.DeepEqual(history, updates) || len(end.Events) != 0 || end.NextSequence != history.NextSequence || !d.controllerProcess.stopped() {
			t.Fatalf("offline event query changed history: history=%+v updates=%+v end=%+v", history, updates, end)
		}
	}

	// Assert: persistence is verified through the public API after a real restart.
	if completed.Status != "completed" || completed.ReportID == "" {
		t.Fatalf("operation not completed: %+v", completed)
	}
	if before.ID != completed.ReportID || before.OperationID != operation.ID || before.Mission != "local-observation" || before.CreatedAt == "" || before.Output.Summary != "local fixture inspected" {
		t.Fatalf("report: %+v", before)
	}
}
