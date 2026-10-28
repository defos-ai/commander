package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
	"github.com/defos-ai/commander/framework/id"
)

func TestAffinityWaitsForItsOwnerWhileAnotherOperationUsesASpare(t *testing.T) {
	a := fixtures.Connect(t)
	for range 2 {
		provision, spawn := runFixtureAction(t, a, "install-agent", "spawn-agent", fixtures.Object{"package": "github.com/defos-ai/fixture-agent@v0.1.0"})
		var installed struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(spawn.Result.Output, &installed); err != nil || installed.AgentID == "" {
			t.Fatalf("fixture installation did not identify its agent: %s: %v", spawn.Result.Output, err)
		}
		decision := fixtures.ModelDecision(a.Wait(provision.ID, func(op models.OperationResponse) bool { return fixtures.ModelDecision(op) != nil }))
		a.Resolve(provision.ID, *decision, []fixtures.Object{})
		fixtures.Post[models.OperationResponse](a, fixtures.OperationPath(provision.ID)+"/complete", fixtures.Object{"output": fixtures.Object{"agent_id": installed.AgentID}}, http.StatusOK)
	}

	op := fixtures.Post[models.OperationResponse](a, "/api/v1/operations", fixtures.Object{"mission_name": "fixture-affinity", "input": fixtures.Object{"message": "bind owner"}}, http.StatusCreated)
	op = a.Wait(op.ID, func(op models.OperationResponse) bool {
		return op.Preflight != nil && op.Preflight.Ready && len(op.Preflight.Actions) == 1 && len(op.Preflight.Actions[0].ReadyAgentIDs) >= 2
	})
	assertAffinityOwner(t, op, "")

	gate := id.OperationID()
	resumeURL := "http://fixture-releases:8080/gates/" + gate
	release := func() {
		// Cleanup still releases the fixture after t.Context is cancelled.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, fixtures.Environment("COMMANDER_TEST_FIXTURE_URL", "http://127.0.0.1:14276")+"/gates/"+gate, nil)
		if err != nil {
			t.Error(err)
			return
		}
		response, err := a.Client.Do(request)
		if err != nil {
			t.Errorf("release affinity readiness gate: %v", err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Errorf("release affinity readiness gate: status=%d", response.StatusCode)
		}
	}
	// Release before shared-state settlement, including on assertion failure.
	t.Cleanup(release)
	first := fixtures.Post[models.TaskResponse](a, fixtures.OperationPath(op.ID)+"/tasks", fixtures.Object{
		"action_name": "fixture-echo", "input": fixtures.Object{"message": "bind owner", "resume_url": resumeURL},
	}, http.StatusCreated)
	blocked := a.Wait(op.ID, func(op models.OperationResponse) bool {
		return fixtures.TasksSettled(op) && op.Preflight != nil && !op.Preflight.Ready
	})
	if len(blocked.Tasks) != 1 {
		t.Fatalf("first execution changed task count: %+v", blocked.Tasks)
	}
	owner := blocked.Tasks[0].AgentID
	if owner == "" {
		t.Fatal("first affinity task has no assigned agent")
	}
	assertAffinityOwner(t, blocked, owner)
	assertAffinityEcho(t, a, blocked.Tasks[0], first, owner, "bind owner")

	second := fixtures.Post[models.TaskResponse](a, fixtures.OperationPath(op.ID)+"/tasks", fixtures.Object{
		"action_name": "fixture-echo", "input": fixtures.Object{"message": "resume original owner"},
	}, http.StatusCreated)
	assertAffinityWaiting(t, fixtures.Read[models.OperationResponse](a, fixtures.OperationPath(op.ID)), second.ID, owner)

	// A separate operation can bind to a ready spare. Its successful execution
	// gives the controller work to process while the original owner stays blocked.
	independent, spareTask := runFixtureAction(t, a, "fixture-affinity", "fixture-echo", fixtures.Object{"message": "independent operation"})
	if spareTask.AgentID == "" || spareTask.AgentID == owner {
		t.Fatalf("independent operation did not select a spare: owner=%s task=%+v", owner, spareTask)
	}
	assertAffinityOwner(t, independent, spareTask.AgentID)
	assertAffinityEcho(t, a, spareTask, spareTask, spareTask.AgentID, "independent operation")
	completedSpare := fixtures.Post[models.OperationResponse](a, fixtures.OperationPath(independent.ID)+"/complete", fixtures.Object{"output": fixtures.Object{}}, http.StatusOK)
	assertAffinityOwner(t, completedSpare, spareTask.AgentID)
	assertAffinityWaiting(t, fixtures.Read[models.OperationResponse](a, fixtures.OperationPath(op.ID)), second.ID, owner)

	release()
	finished := a.Wait(op.ID, fixtures.TasksSettled)
	if len(finished.Tasks) != 2 {
		t.Fatalf("resuming owner added or lost tasks: %+v", finished.Tasks)
	}
	for _, submitted := range []models.TaskResponse{first, second} {
		index := slices.IndexFunc(finished.Tasks, func(task models.TaskResponse) bool { return task.ID == submitted.ID })
		if index < 0 {
			t.Fatalf("resuming owner replaced task %s: %+v", submitted.ID, finished.Tasks)
		}
		message := "bind owner"
		if submitted.ID == second.ID {
			message = "resume original owner"
		}
		assertAffinityEcho(t, a, finished.Tasks[index], submitted, owner, message)
	}
	completed := fixtures.Post[models.OperationResponse](a, fixtures.OperationPath(op.ID)+"/complete", fixtures.Object{"output": fixtures.Object{}}, http.StatusOK)
	if completed.Status != "completed" || completed.ReportID == "" || completedSpare.Status != "completed" || completedSpare.ReportID == "" {
		t.Fatal("affinity operations did not preserve their completion reports")
	}
	assertAffinityOwner(t, completed, owner)
}

func assertAffinityOwner(t *testing.T, op models.OperationResponse, owner string) {
	t.Helper()
	if op.Affinity == nil || op.Affinity.AgentID != owner || op.Affinity.Error != "" || !slices.Equal(op.Affinity.ForActions, []string{"fixture-echo"}) {
		t.Fatalf("operation lost saved affinity: %+v", op.Affinity)
	}
}

func assertAffinityWaiting(t *testing.T, op models.OperationResponse, taskID, owner string) {
	t.Helper()
	assertAffinityOwner(t, op, owner)
	if op.Preflight == nil || op.Preflight.Ready || len(op.Preflight.Actions) != 1 {
		t.Fatalf("unavailable owner did not block preflight: %+v", op.Preflight)
	}
	readiness := op.Preflight.Actions[0]
	if readiness.ActionName != "fixture-echo" || len(readiness.ReadyAgentIDs) != 0 || len(readiness.UnavailableAgents) != 1 ||
		readiness.UnavailableAgents[0].AgentID != owner || readiness.UnavailableAgents[0].Reason == "" {
		t.Fatalf("preflight included an alternative to the bound owner: %+v", readiness)
	}
	index := slices.IndexFunc(op.Tasks, func(task models.TaskResponse) bool { return task.ID == taskID })
	if index < 0 || op.Tasks[index].Status != "queued" || op.Tasks[index].AgentID != "" || op.Tasks[index].DeadlineAt != nil || op.Tasks[index].Result != nil {
		t.Fatalf("blocked task acquired an execution or disappeared: %+v", op.Tasks)
	}
}

func assertAffinityEcho(t *testing.T, a *fixtures.Client, task, submitted models.TaskResponse, owner, message string) {
	t.Helper()
	if task.ID != submitted.ID || task.CommandID != submitted.CommandID || task.OperationID != submitted.OperationID ||
		task.Status != "succeeded" || task.AgentID != owner || task.Result == nil {
		t.Fatalf("affinity execution lost identity or changed owner: submitted=%+v task=%+v", submitted, task)
	}
	var output struct {
		Message string `json:"message"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(task.Result.Output, &output); err != nil || output.Message != message || output.AgentID != owner {
		t.Fatalf("affinity result lost agent output: %s: %v", task.Result.Output, err)
	}
	if len(task.Result.Artifacts) != 1 || len(task.Result.Artifacts[0].Files) != 1 {
		t.Fatalf("affinity result lost uploaded evidence: %+v", task.Result.Artifacts)
	}
	artifact := task.Result.Artifacts[0]
	file := artifact.Files[0]
	download := a.Request(http.MethodGet, "/api/v1/artifacts/"+artifact.ID+"/files/"+file.ID, nil)
	if artifact.AgentID != owner || artifact.CommandID != task.CommandID || file.Name != "echo.txt" || download.Status != http.StatusOK || download.Body != message {
		t.Fatalf("affinity evidence lost ownership or bytes: artifact=%+v download=%+v", artifact, download)
	}
}
