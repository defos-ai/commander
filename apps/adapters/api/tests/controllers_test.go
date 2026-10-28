package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
)

func TestControllersExposeRegisteredStateAndLeaseFreshness(t *testing.T) {
	a := fixtures.Connect(t)
	ctx, cancel := context.WithTimeout(a.Context, 30*time.Second)
	defer cancel()
	var inventory models.ListResponse[models.ControllerResponse]
	for {
		inventory = fixtures.Read[models.ListResponse[models.ControllerResponse]](a, "/api/v1/controllers")
		ready := false
		for _, controller := range inventory.Items {
			ready = ready || controller.Status == "ready" && !controller.Stale
		}
		if ready {
			break
		}
		if err := fixtures.Pause(ctx); err != nil {
			t.Fatalf("controller did not become ready: %+v: %v", inventory.Items, err)
		}
	}

	for i, controller := range inventory.Items {
		if controller.ID == "" || i > 0 && inventory.Items[i-1].ID >= controller.ID {
			t.Fatalf("controller inventory is not sorted by unique ID: %+v", inventory.Items)
		}
		path := "/api/v1/controllers/" + controller.ID
		result := a.Request(http.MethodGet, path, nil)
		detail := fixtures.Decode[models.ControllerResponse](t, result, http.StatusOK)
		if detail.ID != controller.ID || detail.Type == "" {
			t.Fatalf("controller identity is missing: %+v", detail)
		}
		if detail.Version == "" || detail.AgentSelector != "round-robin" || detail.RuleSelector != "mission-rules" {
			t.Fatalf("controller runtime metadata is missing: %+v", detail)
		}
		if detail.Server.Hostname == "" || detail.Server.OS == "" || detail.Server.Arch == "" {
			t.Fatalf("controller server metadata is missing: %+v", detail.Server)
		}
		if detail.StartedAt.IsZero() || detail.LastHeartbeat.Before(detail.StartedAt) || detail.LeaseExpiresAt.Before(detail.LastHeartbeat) || detail.Status != "stopped" && detail.LeaseExpiresAt.Equal(detail.LastHeartbeat) {
			t.Fatalf("controller lifetime or lease is invalid: %+v", detail)
		}
		if detail.Type != controller.Type || detail.Server != controller.Server || detail.Version != controller.Version || detail.AgentSelector != controller.AgentSelector || detail.RuleSelector != controller.RuleSelector || !detail.StartedAt.Equal(controller.StartedAt) {
			t.Fatalf("controller identity changed between list and detail: list=%+v detail=%+v", controller, detail)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(result.Body), &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"version", "agent_selector", "rule_selector"} {
			var value string
			if len(fields[name]) == 0 || string(fields[name]) == "null" || json.Unmarshal(fields[name], &value) != nil {
				t.Fatalf("controller %s must be a JSON string: %s", name, result.Body)
			}
		}
		assertSchema(t, a, "Controller", result)
		assertHeaders(t, result)
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if result := a.Request(method, path, nil); result.Status != http.StatusMethodNotAllowed {
				t.Fatalf("controller accepted unsupported method %s: %+v", method, result)
			}
		}
	}
	for _, id := range []string{"not-valid", "unknown-controller-1234abcd"} {
		fixtures.AssertError(t, a.Request(http.MethodGet, "/api/v1/controllers/"+id, nil), http.StatusNotFound, "unknown_controller")
	}
}
