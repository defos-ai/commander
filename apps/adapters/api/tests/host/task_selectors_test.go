package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestTaskSelectorsRouteThroughControllerAndRemainReadable(t *testing.T) {
	stack := hostfixtures.NewSharingStack(t)
	const targetID, targetHostname = "analyzer-agent-00000004", "selected-host"
	stack.StartAnalyzerOnHost(t, targetID, targetHostname)
	mission := hostfixtures.SharingMission("selected-execution-host", nil)
	decodeSharing[models.MissionResponse](t, stack.Request(t, http.MethodPost, "/api/v1/missions", mission), http.StatusCreated)
	operation := startSharingOperation(t, stack, mission.Name)
	path := "/api/v1/operations/" + operation.ID + "/tasks"
	for _, selector := range []any{
		map[string]any{},
		map[string]any{"hostname": ""},
		map[string]any{"hostname": nil},
		map[string]any{"hostname": " host"},
		map[string]any{"hostname": targetHostname, "unknown": "value"},
		map[string]any{"id": targetID, "hostname": targetHostname},
		map[string]any{"type": "analyzer", "hostname": targetHostname},
		map[string]any{"version": "v1.0.0", "hostname": targetHostname},
	} {
		response := stack.Request(t, http.MethodPost, path, map[string]any{
			"action_name": fakes.AnalysisAction, "input": map[string]any{}, "selector": selector,
		})
		if response.Status != http.StatusBadRequest {
			t.Fatalf("invalid assignment selector was accepted: %d %s", response.Status, response.Body)
		}
	}
	untouched := decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodGet, "/api/v1/operations/"+operation.ID, nil), http.StatusOK)
	if len(untouched.Tasks) != 0 {
		t.Fatalf("invalid selectors submitted work: %+v", untouched.Tasks)
	}
	selector := models.TaskSelectorRequest{Hostname: targetHostname}

	assigned := decodeSharing[models.TaskResponse](t, stack.Request(t, http.MethodPost, path, models.AssignTaskRequest{
		ActionName: fakes.AnalysisAction, Selector: &selector, Input: json.RawMessage(`{}`),
	}), http.StatusCreated)
	working := stack.AwaitWorking(t, operation.ID, assigned.ID)

	want := models.TaskSelectorResponse{Hostname: selector.Hostname}
	if assigned.Selector == nil || *assigned.Selector != want || working.Selector == nil || *working.Selector != want || working.AgentID != targetID {
		t.Fatalf("saved selector did not route to its matching executor: accepted=%+v working=%+v", assigned, working)
	}
	listed := decodeSharing[struct {
		Items []models.TaskResponse `json:"items"`
	}](t, stack.Request(t, http.MethodGet, path, nil), http.StatusOK)
	readback := decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodGet, "/api/v1/operations/"+operation.ID, nil), http.StatusOK)
	if len(listed.Items) != 1 || listed.Items[0].Selector == nil || *listed.Items[0].Selector != want ||
		len(readback.Tasks) != 1 || readback.Tasks[0].Selector == nil || *readback.Tasks[0].Selector != want {
		t.Fatalf("task list or operation readback lost executor criteria: list=%+v operation=%+v", listed.Items, readback.Tasks)
	}
	blocked := decodeSharing[models.TaskResponse](t, stack.Request(t, http.MethodPost, path, models.AssignTaskRequest{
		ActionName: fakes.AnalysisAction, Input: json.RawMessage(`{}`),
		Selector: &models.TaskSelectorRequest{Hostname: "missing-host"},
	}), http.StatusCreated)
	ordinary := decodeSharing[models.TaskResponse](t, stack.Request(t, http.MethodPost, path, map[string]any{
		"action_name": fakes.FindingAction, "input": map[string]any{"marker": "unconstrained-work"}, "selector": nil,
	}), http.StatusCreated)
	completed := stack.AwaitTask(t, operation.ID, ordinary.ID, "succeeded")
	queued := decodeSharing[models.TaskResponse](t, stack.Request(t, http.MethodGet, path+"/"+blocked.ID, nil), http.StatusOK)
	if completed.Selector != nil || queued.Status != "queued" || queued.AgentID != "" || queued.Selector == nil || queued.Selector.Hostname != "missing-host" {
		t.Fatalf("null selector was constrained or unmatched hostname fell back: completed=%+v queued=%+v", completed, queued)
	}

	stops := decodeSharing[models.StopAgentsResponse](t, stack.Request(t, http.MethodPost, "/api/v1/agents/stop", models.StopAgentsRequest{
		Selector: models.StopSelectorRequest{Type: "analyzer", Hostname: targetHostname},
	}), http.StatusAccepted)
	if len(stops.Items) != 1 || stops.Items[0].ID != targetID || len(stops.Errors) != 0 {
		t.Fatalf("hostname stop did not intersect controller inventory: %+v", stops)
	}
	stack.Analyzer(targetID).Stop()
	stack.AwaitTask(t, operation.ID, assigned.ID, "succeeded")
	decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodPost, "/api/v1/operations/"+operation.ID+"/cancel", models.CancelOperationRequest{}), http.StatusOK)
}
