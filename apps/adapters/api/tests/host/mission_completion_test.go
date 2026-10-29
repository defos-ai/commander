package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestMissionCompletionPolicyRoundTripAndOperationSnapshot(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	handler := hostfixtures.LogHandler(t, config)
	body := strings.Replace(hostfixtures.MissionDefinition, `"objectives":`, `"completion":{"decision_by":"operator"},"objectives":`, 1)

	created := decodeResponse[models.MissionResponse](t,
		hostfixtures.MissionRequest(t.Context(), handler, http.MethodPost, "/api/v1/missions", body, true), http.StatusCreated)

	if created.Completion == nil || created.Completion.DecisionBy != "operator" {
		t.Fatal("creation lost the mission completion policy")
	}
	path := "/api/v1/missions/" + url.PathEscape(created.Name)
	read := decodeResponse[models.MissionResponse](t, hostfixtures.LogRequest(t.Context(), handler, path, true))
	listing := decodeResponse[struct {
		Items []models.MissionResponse `json:"items"`
	}](t, hostfixtures.LogRequest(t.Context(), handler, "/api/v1/missions", true))
	if read.Completion == nil || read.Completion.DecisionBy != "operator" || len(listing.Items) != 1 ||
		listing.Items[0].Completion == nil || listing.Items[0].Completion.DecisionBy != "operator" {
		t.Fatal("mission reads lost the saved completion policy")
	}
	mission, err := config.MissionStore.Get(t.Context(), created.Name)
	if err != nil {
		t.Fatal(err)
	}
	operation := hostfixtures.LogOperation()
	operation.Mission = mission.Clone()
	operation.Input = json.RawMessage(`{"domain":"example.org"}`)
	if err := config.OperationStore.Create(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	body = strings.Replace(body, `"decision_by":"operator"`, `"decision_by":"model"`, 1)

	updated := decodeResponse[models.MissionResponse](t,
		hostfixtures.MissionRequest(t.Context(), handler, http.MethodPut, path, body, true))
	saved := decodeResponse[models.OperationResponse](t,
		hostfixtures.LogRequest(t.Context(), handler, "/api/v1/operations/"+operation.ID, true))

	if updated.Completion == nil || updated.Completion.DecisionBy != "model" ||
		saved.Mission.Completion == nil || saved.Mission.Completion.DecisionBy != "operator" {
		t.Fatal("mission editing changed the retained operation completion policy")
	}

	response := hostfixtures.MissionRequest(t.Context(), handler, http.MethodPut, path, hostfixtures.MissionDefinition, true)
	withoutPolicy := decodeResponse[models.MissionResponse](t, response)

	if withoutPolicy.Completion != nil || strings.Contains(response.Body, `"completion"`) {
		t.Fatal("omitting the completion policy did not preserve omission in responses")
	}
}

func TestMissionCompletionPolicyRejectsInvalidDefinitions(t *testing.T) {
	for _, scenario := range []struct{ name, completion string }{
		{"missing decision maker", `{}`},
		{"empty decision maker", `{"decision_by":""}`},
		{"null decision maker", `{"decision_by":null}`},
		{"unsupported decision maker", `{"decision_by":"human"}`},
		{"action-scoped policy", `{"decision_by":"operator","for_actions":["inspect-domain"]}`},
		{"unknown field", `{"decision_by":"operator","timeout_seconds":120}`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := hostfixtures.LogAPIConfig(t)
			handler := hostfixtures.LogHandler(t, config)
			body := strings.Replace(hostfixtures.MissionDefinition, `"objectives":`, `"completion":`+scenario.completion+`,"objectives":`, 1)

			response := hostfixtures.MissionRequest(t.Context(), handler, http.MethodPost, "/api/v1/missions", body, true)

			assertAPIError(t, response, http.StatusBadRequest, "invalid_request")
			all, err := config.MissionStore.List(t.Context())
			if err != nil || len(all) != 0 {
				t.Fatal("invalid completion policy changed the mission catalog")
			}
		})
	}
}
