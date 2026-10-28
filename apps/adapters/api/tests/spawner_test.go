package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
	"github.com/defos-ai/commander/framework/id"
)

func TestSpawnedAgentExecutesOperation(t *testing.T) {
	a := fixtures.Connect(t)
	const reference = "github.com/defos-ai/fixture-agent@v0.1.0"
	provision, spawn := runFixtureAction(t, a, "install-agent", "spawn-agent", fixtures.Object{"package": reference})
	var launched struct {
		AgentID        string                  `json:"agent_id"`
		AgentType      string                  `json:"agent_type"`
		Package        string                  `json:"package"`
		Runtime        string                  `json:"runtime"`
		RuntimeDetails json.RawMessage         `json:"runtime_details"`
		Actions        []models.ActionResponse `json:"actions"`
	}
	decodeSpawnOutput(t, spawn.Result.Output, &launched)
	if launched.AgentID == "" || launched.AgentType != "fixture-agent" || launched.Package != reference || launched.Runtime != fixtures.Environment("COMMANDER_TEST_SPAWNER_RUNTIME", "native") || !slices.Equal(fixtures.ActionNames(launched.Actions), []string{"fixture-echo"}) {
		t.Fatalf("spawn result lost package, runtime or registered capability: %+v", launched)
	}
	switch launched.Runtime {
	case "native":
		var details struct {
			PID int `json:"pid"`
		}
		decodeSpawnOutput(t, launched.RuntimeDetails, &details)
		if details.PID <= 0 {
			t.Fatalf("native spawn did not return its process: %+v", details)
		}
	case "docker":
		var details struct {
			ContainerID string `json:"container_id"`
		}
		decodeSpawnOutput(t, launched.RuntimeDetails, &details)
		if details.ContainerID == "" {
			t.Fatalf("Docker spawn did not return its container: %+v", details)
		}
	case "kubernetes":
		var details struct {
			Namespace string `json:"namespace"`
			PodName   string `json:"pod_name"`
			PodUID    string `json:"pod_uid"`
		}
		decodeSpawnOutput(t, launched.RuntimeDetails, &details)
		if details.Namespace == "" || details.PodUID == "" || !strings.HasSuffix(details.PodName, launched.AgentID) {
			t.Fatalf("Kubernetes spawn did not return its assigned Pod identity: %+v", details)
		}
	default:
		t.Fatalf("unsupported fixture runtime: %q", launched.Runtime)
	}

	// The spawner observes the registration announcement. Wait separately for
	// the controller to accept it before checking public operation readiness.
	ctx, cancel := context.WithTimeout(a.Context, 30*time.Second)
	defer cancel()
	for {
		result := a.Request(http.MethodGet, "/api/v1/agents/"+launched.AgentID, nil)
		if result.Status == http.StatusOK {
			agent := fixtures.Decode[models.AgentResponse](t, result, http.StatusOK)
			if agent.ID == launched.AgentID && agent.Type == launched.AgentType && agent.Status == "ready" && !agent.Stale && slices.Equal(fixtures.ActionNames(agent.Actions), []string{"fixture-echo"}) {
				break
			}
		} else if result.Status != http.StatusNotFound {
			t.Fatalf("query spawned registration: %+v", result)
		}
		if err := fixtures.Pause(ctx); err != nil {
			t.Fatalf("controller did not accept registration for %s: %s: %v", launched.AgentID, result.Body, err)
		}
	}
	decision := fixtures.ModelDecision(a.Wait(provision.ID, func(op models.OperationResponse) bool { return fixtures.ModelDecision(op) != nil }))
	if decision.ID != spawn.ID {
		t.Fatalf("install mission requested a decision for a different task: %+v", decision)
	}
	a.Resolve(provision.ID, *decision, []fixtures.Object{})
	fixtures.Post[models.OperationResponse](a, fixtures.OperationPath(provision.ID)+"/complete", fixtures.Object{"output": fixtures.Object{"agent_id": launched.AgentID}}, http.StatusOK)
	installed := fixtures.Read[models.OperationResponse](a, fixtures.OperationPath(provision.ID))
	var installedOutput struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(installed.Output, &installedOutput); err != nil {
		t.Fatal(err)
	}
	if installed.Status != "completed" || installed.ReportID == "" || installedOutput.AgentID != launched.AgentID || fixtures.ModelDecision(installed) != nil {
		t.Fatalf("install mission did not save the reviewed agent identity: %+v", installed)
	}
	assertSpawnProgressLogs(t, a, provision.ID, spawn, launched.AgentID)

	const message = "Executed through the API on a dynamically spawned agent"
	op := fixtures.Post[models.OperationResponse](a, "/api/v1/operations", fixtures.Object{"mission_name": "fixture-use-agent", "input": fixtures.Object{"message": message}}, http.StatusCreated)
	if op.Preflight == nil || !op.Preflight.Ready || len(op.Preflight.Actions) != 1 || !slices.Contains(op.Preflight.Actions[0].ReadyAgentIDs, launched.AgentID) {
		t.Fatalf("new agent was not available to the next operation: %+v", op.Preflight)
	}
	fixtures.Post[models.TaskResponse](a, fixtures.OperationPath(op.ID)+"/tasks", fixtures.Object{"action_name": "fixture-echo", "input": fixtures.Object{"message": message}}, http.StatusCreated)
	finished := a.Wait(op.ID, fixtures.TasksSettled)
	if len(finished.Tasks) != 1 || finished.Tasks[0].Status != "succeeded" || finished.Tasks[0].Result == nil {
		t.Fatalf("spawned capability did not execute: %+v", finished.Tasks)
	}
	task := finished.Tasks[0]
	fixtures.AssertError(t, a.Request(http.MethodGet, fixtures.OperationPath(provision.ID)+"/logs?task_id="+task.ID, nil), http.StatusNotFound, "task_not_found")
	var echoed struct {
		Message string `json:"message"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(task.Result.Output, &echoed); err != nil {
		t.Fatal(err)
	}
	// Other suites may already have spawned this fixture. The controller owns
	// selection; the result must identify the registered executor it chose.
	executor := fixtures.Read[models.AgentResponse](a, "/api/v1/agents/"+task.AgentID)
	if echoed.Message != message || echoed.AgentID != task.AgentID || executor.Type != "fixture-agent" || !slices.Contains(op.Preflight.Actions[0].ReadyAgentIDs, task.AgentID) {
		t.Fatalf("fixture execution lost input or assigned identity: task=%+v output=%+v agent=%+v", task, echoed, executor)
	}
	if len(task.Result.Artifacts) != 1 || len(task.Result.Artifacts[0].Files) != 1 {
		t.Fatalf("spawned agent did not upload its evidence: %+v", task.Result.Artifacts)
	}
	artifact := task.Result.Artifacts[0]
	file := artifact.Files[0]
	download := a.Request(http.MethodGet, "/api/v1/artifacts/"+artifact.ID+"/files/"+file.ID, nil)
	if artifact.AgentID != task.AgentID || artifact.CommandID != task.CommandID || file.Name != "echo.txt" || file.MediaType != "text/plain" || download.Status != http.StatusOK || download.Body != message {
		t.Fatalf("spawned agent evidence lost bytes or ownership: artifact=%+v download=%+v", artifact, download)
	}
	completed := fixtures.Post[models.OperationResponse](a, fixtures.OperationPath(op.ID)+"/complete", fixtures.Object{"output": fixtures.Object{}}, http.StatusOK)
	if completed.Status != "completed" || completed.ReportID == "" {
		t.Fatalf("fixture operation did not complete: %+v", completed)
	}
}

func assertSpawnProgressLogs(t *testing.T, a *fixtures.Client, operationID string, spawn models.TaskResponse, childAgentID string) {
	t.Helper()
	spawner := fixtures.Read[models.AgentResponse](a, "/api/v1/agents/"+spawn.AgentID)
	if spawner.ID != spawn.AgentID || spawner.Type != "spawner-agent" || spawn.ActionName != "spawn-agent" || spawn.OperationID != operationID {
		t.Fatalf("spawn task lost its assigned execution identity: task=%+v agent=%+v", spawn, spawner)
	}
	path := fixtures.OperationPath(operationID) + "/logs"
	parameters := url.Values{
		"task_id": {spawn.ID}, "agent_id": {spawn.AgentID}, "action_name": {spawn.ActionName},
		"limit": {"1"}, "order": {"asc"}, "cursor": {""},
	}
	ctx, cancel := context.WithTimeout(a.Context, 30*time.Second)
	defer cancel()
	var logs []models.LogResponse
	seen := make(map[string]bool)
	messages := make(map[string]bool)
	for {
		result, err := a.Send(ctx, http.MethodGet, path+"?"+parameters.Encode(), nil)
		if err != nil {
			t.Fatalf("read saved spawn progress: %v", err)
		}
		page := fixtures.Decode[models.PageResponse[models.LogResponse]](t, result, http.StatusOK)
		cursor := parameters.Get("cursor")
		if page.Items == nil || len(page.Items) > 1 {
			t.Fatalf("limit-1 log query returned an invalid page: %+v", page)
		}
		if len(page.Items) == 0 {
			if page.NextCursor != cursor {
				t.Fatalf("empty log page changed cursor %q: %+v", cursor, page)
			}
			// Task results and log events are persisted independently.
			if messages["Package version selection started"] && messages["Package version selection completed"] {
				break
			}
			if err := fixtures.Pause(ctx); err != nil {
				t.Fatalf("spawn progress was not persisted after task success: logs=%+v: %v", logs, err)
			}
			continue
		}
		log := page.Items[0]
		assertSpawnLogIdentity(t, log, operationID, spawn)
		if seen[log.ID] || page.NextCursor != log.ID {
			t.Fatalf("log pagination repeated an ID or lost its cursor: %+v", page)
		}
		seen[log.ID], messages[log.Message] = true, true
		logs = append(logs, log)
		parameters.Set("cursor", page.NextCursor)
	}

	unfiltered := fixtures.Read[models.PageResponse[models.LogResponse]](a, path+"?limit=1000")
	if len(unfiltered.Items) < len(logs) || !slices.EqualFunc(unfiltered.Items[:len(logs)], logs, func(left, right models.LogResponse) bool { return left.ID == right.ID }) {
		t.Fatalf("unfiltered operation logs lost the saved spawn history: %+v", unfiltered)
	}
	seen = make(map[string]bool)
	for _, log := range unfiltered.Items {
		assertSpawnLogIdentity(t, log, operationID, spawn)
		if seen[log.ID] {
			t.Fatalf("operation log query repeated ID %s", log.ID)
		}
		seen[log.ID] = true
	}
	if unfiltered.NextCursor != unfiltered.Items[len(unfiltered.Items)-1].ID {
		t.Fatalf("operation log cursor lost its final ID: %+v", unfiltered)
	}

	// Anchor descending reads at a saved ID so later deliveries cannot change this prefix.
	parameters.Set("order", "desc")
	for index := len(logs) - 2; index >= -1; index-- {
		page := fixtures.Read[models.PageResponse[models.LogResponse]](a, path+"?"+parameters.Encode())
		if index == -1 {
			if page.Items == nil || len(page.Items) != 0 || page.NextCursor != parameters.Get("cursor") {
				t.Fatalf("empty descending log page lost its cursor: %+v", page)
			}
			break
		}
		if len(page.Items) != 1 || page.Items[0].ID != logs[index].ID || page.NextCursor != logs[index].ID {
			t.Fatalf("descending log page differs from ascending history at %d: %+v", index, page)
		}
		assertSpawnLogIdentity(t, page.Items[0], operationID, spawn)
		parameters.Set("cursor", page.NextCursor)
	}
	parameters.Del("cursor")
	for key, value := range map[string]string{"agent_id": childAgentID, "action_name": "fixture-echo"} {
		original := parameters.Get(key)
		parameters.Set(key, value)
		page := fixtures.Read[models.PageResponse[models.LogResponse]](a, path+"?"+parameters.Encode())
		if page.Items == nil || len(page.Items) != 0 || page.NextCursor != "" {
			t.Fatalf("%s filter leaked another execution's logs: %+v", key, page)
		}
		parameters.Set(key, original)
	}
}

func assertSpawnLogIdentity(t *testing.T, log models.LogResponse, operationID string, spawn models.TaskResponse) {
	t.Helper()
	if !id.Valid(log.ID) || !id.Valid(log.CommandID) || !id.Valid(log.OperationID) || !id.Valid(log.TaskID) ||
		log.OperationID != operationID || log.TaskID != spawn.ID || log.AgentID != spawn.AgentID ||
		log.CommandID != spawn.CommandID || log.ActionName != spawn.ActionName || log.StepID != spawn.StepID || log.CreatedAt.IsZero() {
		t.Fatalf("saved log lost its execution provenance: log=%+v task=%+v", log, spawn)
	}
}

func decodeSpawnOutput(t *testing.T, data []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode spawn output: %v", err)
	}
}

func runFixtureAction(t *testing.T, a *fixtures.Client, mission, action string, input fixtures.Object) (models.OperationResponse, models.TaskResponse) {
	t.Helper()
	op := fixtures.Post[models.OperationResponse](a, "/api/v1/operations", fixtures.Object{"mission_name": mission, "input": input}, http.StatusCreated)
	if op.Preflight == nil || !op.Preflight.Ready {
		t.Fatalf("fixture mission is unavailable: %+v", op.Preflight)
	}
	fixtures.Post[models.TaskResponse](a, fixtures.OperationPath(op.ID)+"/tasks", fixtures.Object{"action_name": action, "input": input}, http.StatusCreated)
	finished := a.Wait(op.ID, fixtures.TasksSettled)
	if len(finished.Tasks) != 1 || finished.Tasks[0].Status != "succeeded" || finished.Tasks[0].Result == nil {
		t.Fatalf("fixture action %s failed: %+v", action, finished.Tasks)
	}
	return finished, finished.Tasks[0]
}
