package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Decode only the public fields that matter to recovery. These tests do not
// import runner, controller, or provider implementations.
type recoveryOperation struct {
	ID, Status string
	Tasks      []recoveryTask
	Questions  []struct {
		ID, Text, Answer string
		AnsweredAt       *string `json:"answered_at"`
	}
}

type recoveryTask struct {
	ID, Status, Error string
	CommandID         string `json:"command_id"`
	Result            *struct {
		Output         json.RawMessage
		OutputArtifact *struct{ ID string } `json:"output_artifact"`
	}
}

func seedMission(t *testing.T, d *deployment) {
	t.Helper()
	database := filepath.Join(d.root, "database")
	if err := os.MkdirAll(database, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(database, "missions.json"), []byte(missionCatalog), 0600); err != nil {
		t.Fatal(err)
	}
}

func startMission(t *testing.T, d *deployment, session *mcp.ClientSession) recoveryOperation {
	t.Helper()
	return decode[recoveryOperation](t, call(t, d.ctx, session, "start_operation", map[string]any{
		"mission_name": "local-observation", "input": map[string]any{},
	}))
}

func getMission(t *testing.T, d *deployment, session *mcp.ClientSession, operationID string) recoveryOperation {
	t.Helper()
	return decode[recoveryOperation](t, call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": operationID}))
}

func assignLinkCheck(t *testing.T, d *deployment, session *mcp.ClientSession, operationID, agentID, target string) recoveryTask {
	t.Helper()
	return decode[recoveryTask](t, call(t, d.ctx, session, "assign_task", map[string]any{
		"operation_id": operationID, "agent_id": agentID, "action_name": "check-links",
		"input": map[string]any{"url": target},
	}))
}

func waitTask(t *testing.T, d *deployment, session *mcp.ClientSession, operationID, taskID, status string) recoveryOperation {
	t.Helper()
	var operation recoveryOperation
	poll(t, func() bool {
		operation = getMission(t, d, session, operationID)
		for _, task := range operation.Tasks {
			if task.ID != taskID {
				continue
			}
			if task.Status == "running" || task.Status == "queued" {
				return false
			}
			if task.Status != status {
				t.Fatalf("task %s ended as %s, want %s: %s", taskID, task.Status, status, task.Error)
			}
			return true
		}
		t.Fatalf("operation %s lost task %s: %+v", operationID, taskID, operation)
		return false
	})
	return operation
}

func assertTaskEvidence(t *testing.T, d *deployment, session *mcp.ClientSession, task recoveryTask, want string) {
	t.Helper()
	if task.Result == nil || task.Result.OutputArtifact == nil || task.Result.OutputArtifact.ID == "" {
		t.Fatalf("task has no stored output: %+v", task)
	}
	resource, err := session.ReadResource(d.ctx, &mcp.ReadResourceParams{URI: "missions://artifacts/" + task.Result.OutputArtifact.ID})
	if err != nil || len(resource.Contents) != 1 {
		t.Fatalf("read recovered task artifact: %+v, %v", resource, err)
	}
	var observation struct {
		URL        string `json:"url"`
		StatusCode int    `json:"status_code"`
	}
	if err := json.Unmarshal([]byte(resource.Contents[0].Text), &observation); err != nil {
		t.Fatal(err)
	}
	if observation.URL != want+"/" || observation.StatusCode != http.StatusOK {
		t.Fatalf("recovered evidence = %+v, want HTTP 200 and %q", observation, want)
	}
}

func TestMCPResumesQuestionsAndPreservesCompletedWork(t *testing.T) {
	for _, crash := range []bool{false, true} {
		name := "graceful restart"
		if crash {
			name = "crash restart"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange: one finished task, an unanswered question, and a separately
			// cancelled operation are all persisted through the public MCP API.
			d := newDeployment(t)
			seedMission(t, d)
			var requests atomic.Int32
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, "evidence survives restart")
			}))
			defer fixture.Close()
			endpoint, adapter := d.mcp(t, false)
			d.agent(t)
			session := connect(t, d.ctx, endpoint)
			agent := readyAgents(t, d, session, 1)[0]
			operation := startMission(t, d, session)
			task := assignLinkCheck(t, d, session, operation.ID, agent.ID, fixture.URL)
			waitTask(t, d, session, operation.ID, task.ID, "succeeded")
			question := decode[struct{ ID string }](t, call(t, d.ctx, session, "ask_question", map[string]any{
				"operation_id": operation.ID, "text": "Collect one more observation?",
			}))
			before := getMission(t, d, session, operation.ID)
			cancelled := startMission(t, d, session)
			call(t, d.ctx, session, "cancel_operation", map[string]any{"operation_id": cancelled.ID})

			// Act: replace the real controller process while the same agent remains alive.
			if crash {
				d.controllerProcess.kill(t)
			} else {
				d.controllerProcess.interrupt(t)
			}
			d.controller(t)
			if adapter.stopped() {
				t.Fatal("controller restart stopped MCP")
			}
			after := getMission(t, d, session, operation.ID)

			// Assert: restoration preserves completed work and question identity;
			// a pending question still blocks new work and final completion.
			if after.ID != before.ID || after.Status != "waiting" || !reflect.DeepEqual(after.Tasks, before.Tasks) || !reflect.DeepEqual(after.Questions, before.Questions) {
				t.Fatalf("restart changed waiting operation:\nbefore %+v\nafter %+v", before, after)
			}
			assertTaskEvidence(t, d, session, after.Tasks[0], fixture.URL)
			assignment := map[string]any{"operation_id": operation.ID, "agent_id": agent.ID, "action_name": "check-links", "input": map[string]any{"url": fixture.URL}}
			rejected(t, d.ctx, session, "assign_task", assignment)
			rejected(t, d.ctx, session, "complete_operation", map[string]any{"operation_id": operation.ID, "output": map[string]any{"summary": "premature"}})
			if current := getMission(t, d, session, cancelled.ID); current.Status != "cancelled" {
				t.Fatalf("restart reopened cancelled operation: %+v", current)
			}
			assignment["operation_id"] = cancelled.ID
			rejected(t, d.ctx, session, "assign_task", assignment)

			// Act: answer the original question and continue the original operation.
			call(t, d.ctx, session, "answer_question", map[string]any{"operation_id": operation.ID, "question_id": question.ID, "answer": "yes"})
			next := assignLinkCheck(t, d, session, operation.ID, agent.ID, fixture.URL)
			waitTask(t, d, session, operation.ID, next.ID, "succeeded")
			completed := decode[operationView](t, call(t, d.ctx, session, "complete_operation", map[string]any{"operation_id": operation.ID, "output": map[string]any{"summary": "continued after restart"}}))

			// Assert: only the two explicit assignments executed; a final report
			// can be produced without replacing the operation or its first task.
			if completed.Status != "completed" || completed.ReportID == "" || len(completed.Tasks) != 2 || completed.Tasks[0].ID != task.ID || requests.Load() != 2 {
				t.Fatalf("operation did not continue cleanly: %+v, HTTP requests=%d", completed, requests.Load())
			}
		})
	}
}

// controlledHTTP models an already-dispatched request. Release explicitly
// controls whether the agent finishes while the controller is offline or after recovery.
func controlledHTTP(t *testing.T) (string, <-chan struct{}, func(), *atomic.Int32) {
	t.Helper()
	started, released := make(chan struct{}), make(chan struct{})
	var onceStarted, onceReleased sync.Once
	var requests atomic.Int32
	release := func() { onceReleased.Do(func() { close(released) }) }
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		onceStarted.Do(func() { close(started) })
		select {
		case <-released:
			_, _ = io.WriteString(w, "one execution across restart")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { release(); fixture.Close() })
	return fixture.URL, started, release, &requests
}

func waitActionAcknowledged(t *testing.T, agentProcess *process, commandID string) {
	t.Helper()
	// Acknowledging the command confirms the agent finished processing it.
	// This is separate from the controller's receipt for the saved result.
	poll(t, func() bool {
		data, _ := os.ReadFile(agentProcess.logPath)
		completed, acknowledged := false, false
		for _, line := range bytes.Split(data, []byte("\n")) {
			var entry struct {
				Message     string `json:"msg"`
				CommandID   string `json:"command_id"`
				MessageID   string `json:"message_id"`
				MessageType string `json:"message_type"`
			}
			if json.Unmarshal(line, &entry) != nil {
				continue
			}
			if entry.Message == "action completed" && entry.CommandID == commandID {
				completed = true
			}
			if entry.Message == "message acknowledged" && entry.MessageType == "action.run" && entry.MessageID == commandID {
				acknowledged = true
			}
		}
		return completed && acknowledged
	})
}

func TestMCPRestartDoesNotStopActiveOperation(t *testing.T) {
	// Arrange: real agent work is blocked while MCP returns the accepted task.
	d := newDeployment(t)
	d.env["LOG_LEVEL"] = "debug"
	seedMission(t, d)
	target, started, release, requests := controlledHTTP(t)
	endpoint, adapter := d.mcp(t, false)
	controller := d.controllerProcess
	agentProcess := d.agent(t)
	session := connect(t, d.ctx, endpoint)
	agent := readyAgents(t, d, session, 1)[0]
	operation := startMission(t, d, session)
	task := assignLinkCheck(t, d, session, operation.ID, agent.ID, target)
	poll(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})

	// Act: crash only MCP. Finish the action while no adapter is available.
	adapter.kill(t)
	release()
	poll(t, func() bool {
		data, _ := os.ReadFile(agentProcess.logPath)
		for _, line := range bytes.Split(data, []byte("\n")) {
			var entry struct {
				Message       string `json:"msg"`
				MessageType   string `json:"message_type"`
				CorrelationID string `json:"correlation_id"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.Message == "message acknowledged" &&
				entry.MessageType == "action.result.accepted" && entry.CorrelationID == task.CommandID {
				return true
			}
		}
		return false
	})
	endpoint, replacementAdapter := d.mcp(t, false)
	session = connect(t, d.ctx, endpoint)
	completed := waitTask(t, d, session, operation.ID, task.ID, "succeeded")
	registered := readyAgents(t, d, session, 1)

	// Assert: the existing controller accepted the result during the outage;
	// reconnecting MCP preserves the same task, agent, and single execution.
	if replacementAdapter.id == adapter.id {
		t.Fatalf("MCP restart reused runtime ID %q", adapter.id)
	}
	if d.controllerProcess != controller || controller.stopped() || agentProcess.stopped() ||
		registered[0].ID != agent.ID || len(completed.Tasks) != 1 || requests.Load() != 1 {
		t.Fatalf("MCP restart changed active work: operation=%+v, HTTP requests=%d", completed, requests.Load())
	}
	assertTaskEvidence(t, d, session, completed.Tasks[0], target)
}

func TestMCPRecoversDispatchedTaskWithoutExecutingAgain(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		crash         bool
		finishOffline bool
	}{
		{"result completed while the controller was down", true, true},
		{"agent still running after controller crash", true, false},
		{"agent still running after graceful controller stop", false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: the fixture confirms execution started before the controller stops.
			d := newDeployment(t)
			d.env["LOG_LEVEL"] = "debug"
			seedMission(t, d)
			target, started, release, requests := controlledHTTP(t)
			endpoint, adapter := d.mcp(t, false)
			agentProcess := d.agent(t)
			session := connect(t, d.ctx, endpoint)
			agent := readyAgents(t, d, session, 1)[0]
			operation := startMission(t, d, session)
			task := assignLinkCheck(t, d, session, operation.ID, agent.ID, target)
			poll(t, func() bool {
				select {
				case <-started:
					return true
				default:
					return false
				}
			})

			// Act: stop the controller independently of MCP and the agent, then recover its database.
			if scenario.crash {
				d.controllerProcess.kill(t)
			} else {
				d.controllerProcess.interrupt(t)
			}
			if scenario.finishOffline {
				release()
				waitActionAcknowledged(t, agentProcess, task.CommandID)
			}
			d.controller(t)
			if adapter.stopped() {
				t.Fatal("controller restart stopped MCP")
			}
			if !scenario.finishOffline {
				current := getMission(t, d, session, operation.ID)
				if len(current.Tasks) != 1 || current.Tasks[0].ID != task.ID || current.Tasks[0].Status != "running" {
					t.Fatalf("recovery did not resume waiting for running task: %+v", current)
				}
				release()
			}
			recovered := waitTask(t, d, session, operation.ID, task.ID, "succeeded")

			// Assert: the same task and its durable artifact are returned, without
			// issuing another HTTP request or replacing the original operation.
			if len(recovered.Tasks) != 1 || requests.Load() != 1 || agentProcess.stopped() {
				t.Fatalf("recovery duplicated or lost work: %+v, HTTP requests=%d, agent stopped=%t", recovered, requests.Load(), agentProcess.stopped())
			}
			assertTaskEvidence(t, d, session, recovered.Tasks[0], target)
			completed := decode[operationView](t, call(t, d.ctx, session, "complete_operation", map[string]any{"operation_id": operation.ID, "output": map[string]any{"summary": "recovered existing result"}}))
			if completed.Status != "completed" {
				t.Fatalf("recovered operation could not complete: %+v", completed)
			}
		})
	}
}

func TestMCPRecoveryRequiresNewAssignmentAfterAgentLoss(t *testing.T) {
	for _, finishOffline := range []bool{false, true} {
		name := "agent lost during execution"
		if finishOffline {
			name = "agent lost after finishing while the controller was down"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange: a task has been dispatched when the coordinator crashes.
			d := newDeployment(t)
			d.env["LOG_LEVEL"] = "debug"
			seedMission(t, d)
			target, started, release, requests := controlledHTTP(t)
			d.controller(t, "--heartbeat-timeout=600ms")
			endpoint, adapter := d.mcp(t, false)
			agentProcess := d.agent(t)
			session := connect(t, d.ctx, endpoint)
			agent := readyAgents(t, d, session, 1)[0]
			operation := startMission(t, d, session)
			task := assignLinkCheck(t, d, session, operation.ID, agent.ID, target)
			poll(t, func() bool {
				select {
				case <-started:
					return true
				default:
					return false
				}
			})

			// Act: restart the controller after the executing agent disappears. Start a replacement
			// worker as well; its availability must not automatically retry the old task.
			d.controllerProcess.kill(t)
			if finishOffline {
				release()
				waitActionAcknowledged(t, agentProcess, task.CommandID)
			}
			agentProcess.kill(t)
			release()
			d.controller(t)
			if adapter.stopped() {
				t.Fatal("controller restart stopped MCP")
			}
			d.agent(t)
			recovered := waitTask(t, d, session, operation.ID, task.ID, "interrupted")
			replacement := readyAgents(t, d, session, 1)[0]

			// Assert: uncertainty is explicit, with no fabricated result or automatic
			// retry on the replacement worker.
			if recovered.Status == "completed" || recovered.Status == "cancelled" || len(recovered.Tasks) != 1 || recovered.Tasks[0].Error == "" || recovered.Tasks[0].Result != nil || requests.Load() != 1 {
				t.Fatalf("lost execution was not preserved as interrupted: %+v, HTTP requests=%d", recovered, requests.Load())
			}
			if replacement.ID == agent.ID {
				t.Fatal("replacement process reused lost agent identity")
			}

			// Act: the decision maker explicitly chooses a fresh assignment.
			next := assignLinkCheck(t, d, session, operation.ID, replacement.ID, target)
			finished := waitTask(t, d, session, operation.ID, next.ID, "succeeded")

			// Assert: the interrupted attempt remains visible beside its replacement.
			if next.ID == task.ID || len(finished.Tasks) != 2 || finished.Tasks[0].ID != task.ID || finished.Tasks[0].Status != "interrupted" || requests.Load() != 2 {
				t.Fatalf("replacement overwrote the interrupted attempt: %+v, HTTP requests=%d", finished, requests.Load())
			}
			assertTaskEvidence(t, d, session, finished.Tasks[1], target)
		})
	}
}

func TestMCPRecoveryCollectsInflightResultsForTerminalOperations(t *testing.T) {
	for _, scenario := range []struct {
		name, action, status string
	}{
		{"cancelled", "cancel_operation", "cancelled"},
		{"failed", "fail_operation", "failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: ending an operation prevents future assignments while its
			// already-dispatched request remains in progress on the live agent.
			d := newDeployment(t)
			seedMission(t, d)
			target, started, release, requests := controlledHTTP(t)
			endpoint, adapter := d.mcp(t, false)
			d.agent(t)
			session := connect(t, d.ctx, endpoint)
			agent := readyAgents(t, d, session, 1)[0]
			operation := startMission(t, d, session)
			task := assignLinkCheck(t, d, session, operation.ID, agent.ID, target)
			poll(t, func() bool {
				select {
				case <-started:
					return true
				default:
					return false
				}
			})
			arguments := map[string]any{"operation_id": operation.ID}
			if scenario.action == "fail_operation" {
				arguments["reason"] = "Decision maker ended the operation"
			}
			call(t, d.ctx, session, scenario.action, arguments)

			// Act: crash and replace the controller, then let the original remote work finish.
			d.controllerProcess.kill(t)
			d.controller(t)
			if adapter.stopped() {
				t.Fatal("controller restart stopped MCP")
			}
			restored := getMission(t, d, session, operation.ID)
			if restored.Status != scenario.status || len(restored.Tasks) != 1 || restored.Tasks[0].ID != task.ID || restored.Tasks[0].Status != "running" {
				t.Fatalf("recovery changed terminal operation or its active task: %+v", restored)
			}
			release()
			finished := waitTask(t, d, session, operation.ID, task.ID, "succeeded")

			// Assert: evidence is retained without reopening the operation,
			// allowing another assignment, or repeating the original HTTP request.
			if finished.Status != scenario.status || len(finished.Tasks) != 1 || requests.Load() != 1 {
				t.Fatalf("recovered result reopened or duplicated terminal work: %+v, HTTP requests=%d", finished, requests.Load())
			}
			assertTaskEvidence(t, d, session, finished.Tasks[0], target)
			rejected(t, d.ctx, session, "assign_task", map[string]any{
				"operation_id": operation.ID, "agent_id": agent.ID, "action_name": "check-links", "input": map[string]any{"url": target},
			})
		})
	}
}
