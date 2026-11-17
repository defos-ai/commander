package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func TestAgentStopThroughAPIDrainsItsWorkAndPreservesOtherAgents(t *testing.T) {
	stack := hostfixtures.NewSharingStack(t)
	mission := hostfixtures.SharingMission("stop-active-agent", nil)
	decodeSharing[models.MissionResponse](t, stack.Request(t, http.MethodPost, "/api/v1/missions", mission), http.StatusCreated)
	operation := startSharingOperation(t, stack, mission.Name)
	gate := stack.Producer.BlockNextRun()
	task := assignSharingTask(t, stack, operation.ID, fakes.FindingAction, json.RawMessage(`{"marker":"drained-finding"}`))
	awaitSharingGate(t, stack, gate)
	task = stack.AwaitWorking(t, operation.ID, task.ID)
	path := "/api/v1/agents/stop"
	agentPath := "/api/v1/agents/" + task.AgentID
	input := models.StopAgentsRequest{Selector: models.AgentSelectorRequest{ID: task.AgentID}}

	unauthenticated := stack.Request(t, http.MethodPost, path, input, func(request *http.Request) { request.Header.Del("Authorization") })
	if unauthenticated.Status != http.StatusUnauthorized {
		t.Fatalf("stop accepted an unauthenticated caller: %d %s", unauthenticated.Status, unauthenticated.Body)
	}
	foreignOrigin := stack.Request(t, http.MethodPost, path, input, func(request *http.Request) { request.Header.Set("Origin", "https://outside.invalid") })
	if foreignOrigin.Status != http.StatusForbidden {
		t.Fatalf("stop accepted an unapproved Origin: %d %s", foreignOrigin.Status, foreignOrigin.Body)
	}
	for _, invalid := range []struct {
		path string
		body any
	}{
		{path + "?version=v1.0.0", input},
		{path, nil},
		{path, struct{}{}},
		{path, map[string]any{"selector": nil}},
		{path, map[string]any{"selector": map[string]any{}}},
		{path, map[string]any{"selector": map[string]any{"id": "invalid"}}},
		{path, map[string]any{"selector": map[string]any{"id": task.AgentID, "version": ""}}},
		{path, map[string]any{"selector": map[string]any{"id": task.AgentID, "version": nil}}},
		{path, map[string]any{"selector": map[string]any{"id": nil, "type": "producer"}}},
		{path, map[string]any{"selector": map[string]any{"type": "producer", "unknown": "value"}}},
		{path, map[string]any{"selector": map[string]any{"id": task.AgentID}, "force": true}},
		{path, json.RawMessage(`null`)},
		{path, []any{}},
	} {
		response := stack.Request(t, http.MethodPost, invalid.path, invalid.body)
		if response.Status != http.StatusBadRequest {
			t.Fatalf("invalid stop request was accepted: %d %s", response.Status, response.Body)
		}
	}
	before := decodeSharing[models.AgentResponse](t, stack.Request(t, http.MethodGet, "/api/v1/agents/"+task.AgentID, nil), http.StatusOK)
	if before.Status != "working" || before.StopCommandID != "" || !before.StopRequestedAt.IsZero() {
		t.Fatalf("rejected requests changed the working agent: %+v", before)
	}

	response := stack.Request(t, http.MethodPost, path, input)
	accepted := decodeSharing[models.StopAgentsResponse](t, response, http.StatusAccepted)
	if len(accepted.Items) != 1 || len(accepted.Errors) != 0 {
		t.Fatalf("exact ID stop changed its target set: %+v", accepted)
	}
	requested := accepted.Items[0]

	if requested.ID != task.AgentID || requested.Status != "stop_requested" || requested.StopCommandID == "" || requested.StopRequestedAt.IsZero() ||
		requested.Work == nil || requested.Work.TaskID != task.ID || response.Header.Get("Location") != "" {
		t.Fatalf("stop confused acceptance with shutdown or lost its exact target: %+v, Location=%q", requested, response.Header.Get("Location"))
	}
	repeatedResult := decodeSharing[models.StopAgentsResponse](t, stack.Request(t, http.MethodPost, path, input), http.StatusAccepted)
	if len(repeatedResult.Items) != 1 || len(repeatedResult.Errors) != 0 {
		t.Fatalf("repeated stop changed its target set: %+v", repeatedResult)
	}
	repeated := repeatedResult.Items[0]
	readback := decodeSharing[models.AgentResponse](t, stack.Request(t, http.MethodGet, agentPath, nil), http.StatusOK)
	if repeated.StopCommandID != requested.StopCommandID || !repeated.StopRequestedAt.Equal(requested.StopRequestedAt) || readback.Status != "stop_requested" ||
		readback.StopCommandID != requested.StopCommandID || !readback.StopRequestedAt.Equal(requested.StopRequestedAt) {
		t.Fatal("repeated stop or saved readback changed the accepted request")
	}
	other := decodeSharing[models.AgentResponse](t, stack.Request(t, http.MethodGet, "/api/v1/agents/analyzer-agent-00000001", nil), http.StatusOK)
	if other.Status != "ready" || other.StopCommandID != "" || !other.StopRequestedAt.IsZero() {
		t.Fatalf("stopping one agent changed another agent: %+v", other)
	}
	gate.Release()

	completed := stack.AwaitTask(t, operation.ID, task.ID, "succeeded")
	if completed.Result == nil || !strings.Contains(string(completed.Result.Output), "drained-finding") || len(completed.Result.Artifacts) != 1 {
		t.Fatalf("graceful stop lost the accepted task result or artifact: %+v", completed)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		readback = decodeSharing[models.AgentResponse](t, stack.Request(t, http.MethodGet, "/api/v1/agents/"+task.AgentID, nil), http.StatusOK)
		if readback.Status == "stopped" {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-deadline.C:
			t.Fatalf("agent did not confirm stop after its result was accepted: %+v", readback)
		case <-stack.Context.Done():
			t.Fatal("stop confirmation exceeded fixture lifetime")
		}
	}
	confirmedResult := decodeSharing[models.StopAgentsResponse](t, stack.Request(t, http.MethodPost, path, input), http.StatusOK)
	if len(confirmedResult.Items) != 1 || len(confirmedResult.Errors) != 0 {
		t.Fatalf("confirmed stop changed its target set: %+v", confirmedResult)
	}
	confirmed := confirmedResult.Items[0]
	if confirmed.Status != "stopped" || confirmed.StopCommandID != requested.StopCommandID || !confirmed.StopRequestedAt.Equal(requested.StopRequestedAt) || confirmed.Work != nil {
		t.Fatalf("confirmed stop did not remain idempotent: %+v", confirmed)
	}
	matching := models.StopAgentsRequest{Selector: models.AgentSelectorRequest{Type: "analyzer", Version: "v1.0.0"}}
	batch := decodeSharing[models.StopAgentsResponse](t, stack.Request(t, http.MethodPost, path, matching), http.StatusAccepted)
	if len(batch.Items) != 2 || len(batch.Errors) != 0 || batch.Items[0].ID != "analyzer-agent-00000001" || batch.Items[1].ID != "analyzer-agent-00000003" {
		t.Fatalf("type and version filters did not intersect: %+v", batch)
	}
	for _, item := range batch.Items {
		if item.Type != "analyzer" || item.Version != "v1.0.0" || item.Status != "stop_requested" {
			t.Fatalf("batch stop changed matched metadata or claimed shutdown: %+v", item)
		}
	}
	nonmatching := decodeSharing[models.AgentResponse](t, stack.Request(t, http.MethodGet, "/api/v1/agents/analyzer-agent-00000002", nil), http.StatusOK)
	if nonmatching.Status != "ready" || nonmatching.Version != "v2.0.0" || nonmatching.StopCommandID != "" {
		t.Fatalf("version-filtered stop touched a different version: %+v", nonmatching)
	}
	missingInput := models.StopAgentsRequest{Selector: models.AgentSelectorRequest{ID: "producer-agent-ffffffff"}}
	missing := decodeSharing[models.StopAgentsResponse](t, stack.Request(t, http.MethodPost, path, missingInput), http.StatusOK)
	if missing.Items == nil || len(missing.Items) != 0 || len(missing.Errors) != 0 {
		t.Fatalf("no matches did not return an empty result: %+v", missing)
	}
	for _, literal := range []string{"stable", "latest"} {
		selection := models.StopAgentsRequest{Selector: models.AgentSelectorRequest{Version: literal}}
		unresolved := decodeSharing[models.StopAgentsResponse](t, stack.Request(t, http.MethodPost, path, selection), http.StatusOK)
		if unresolved.Items == nil || len(unresolved.Items) != 0 || len(unresolved.Errors) != 0 {
			t.Fatalf("literal version %q was resolved as a release alias: %+v", literal, unresolved)
		}
	}
	removed := stack.Request(t, http.MethodPost, agentPath+"/stop", nil)
	if removed.Status != http.StatusNotFound {
		t.Fatalf("removed exact-ID route remained available: %d %s", removed.Status, removed.Body)
	}
}

func TestAgentStopUncertaintyIsSanitizedAndReadableWithoutRetry(t *testing.T) {
	inventory := hostfixtures.NewAgentInventory(t, t.TempDir())
	record := hostfixtures.AgentDependencyRecord()
	if err := inventory.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	commands := fakes.NewUncertainAgentStop(inventory)
	server := hostfixtures.NewAgentStopAPI(t, inventory, commands)
	path := "/api/v1/agents/" + record.ID
	input := models.StopAgentsRequest{Selector: models.AgentSelectorRequest{ID: record.ID}}

	response := server.Request(t, http.MethodPost, "/api/v1/agents/stop", hostfixtures.JSONBody(t, input))

	if response.Status != http.StatusServiceUnavailable || !strings.Contains(response.Body, "agent_stop_uncertain") || strings.Contains(response.Body, fakes.AgentStopFailureDetail) || commands.Calls() != 1 {
		t.Fatalf("uncertain stop leaked details, retried, or claimed rejection/success: %d %s, calls=%d", response.Status, response.Body, commands.Calls())
	}
	stored := decodeResponse[models.AgentResponse](t, server.Request(t, http.MethodGet, path))
	if stored.Status != "stop_requested" || stored.StopCommandID == "" || stored.StopRequestedAt.IsZero() || stored.ID != record.ID {
		t.Fatalf("uncertain response hid the accepted stop from readback: %+v", stored)
	}
	readOnly := hostfixtures.NewAgentStopAPI(t, inventory, nil)
	unavailable := readOnly.Request(t, http.MethodPost, "/api/v1/agents/stop", hostfixtures.JSONBody(t, input))
	if unavailable.Status != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body, "agent_controls_unavailable") {
		t.Fatalf("missing command capability did not fail explicitly: %d %s", unavailable.Status, unavailable.Body)
	}
	unchanged := decodeResponse[models.AgentResponse](t, readOnly.Request(t, http.MethodGet, path))
	if unchanged.StopCommandID != stored.StopCommandID || !unchanged.StopRequestedAt.Equal(stored.StopRequestedAt) || commands.Calls() != 1 {
		t.Fatal("unavailable controls changed saved state or disabled agent queries")
	}
	accepted, err := inventory.Get(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	failures := []agents.StopError{{ID: "browser-agent-ffffffff", Error: "agent_stop_uncertain"}}
	partialServer := hostfixtures.NewAgentStopAPI(t, inventory, fakes.NewFixedAgentStops(agents.StopResult{Agents: []agents.Agent{accepted}, Errors: failures}))
	partialInput := models.StopAgentsRequest{Selector: models.AgentSelectorRequest{Type: "browser-agent"}}
	partial := decodeSharing[models.StopAgentsResponse](t, partialServer.Request(t, http.MethodPost, "/api/v1/agents/stop", hostfixtures.JSONBody(t, partialInput)), http.StatusAccepted)
	if len(partial.Items) != 1 || len(partial.Errors) != 1 || partial.Errors[0].Error != "agent_stop_uncertain" || partial.Errors[0].ID != failures[0].ID {
		t.Fatalf("partial stop result discarded accepted work or per-agent failure: %+v", partial)
	}
	failedServer := hostfixtures.NewAgentStopAPI(t, inventory, fakes.NewFixedAgentStops(agents.StopResult{Agents: []agents.Agent{}, Errors: failures}))
	failed := decodeSharing[models.StopAgentsResponse](t, failedServer.Request(t, http.MethodPost, "/api/v1/agents/stop", hostfixtures.JSONBody(t, partialInput)), http.StatusOK)
	if failed.Items == nil || len(failed.Items) != 0 || len(failed.Errors) != 1 || failed.Errors[0].Error != "agent_stop_uncertain" {
		t.Fatalf("all per-agent failures were reported as confirmed stops: %+v", failed)
	}
}
