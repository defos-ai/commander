package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/missions"
)

func TestUpdateMissionReplacesDefinitionAndPreservesOperationSnapshot(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	mission, err := missions.LoadMission([]byte(hostfixtures.MissionDefinition))
	if err != nil {
		t.Fatal(err)
	}
	if err := config.MissionStore.Create(t.Context(), mission); err != nil {
		t.Fatal(err)
	}
	operation := hostfixtures.LogOperation()
	operation.Mission = mission.Clone()
	operation.Input = json.RawMessage(`{"domain":"example.org"}`)
	if err := config.OperationStore.Create(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	operation, err = config.OperationStore.Get(t.Context(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/missions/" + url.PathEscape(mission.Name)
	body := strings.ReplaceAll(hostfixtures.MissionDefinition, "inspect-domain", "inspect-updated-domain")
	body = strings.Replace(body, "Review an action that has not been implemented yet.", "Investigate the updated objectives.", 1)
	body = strings.Replace(body, "Inspect a requested domain.", "Inspect the domain and retain detailed evidence.", 1)
	body = strings.Replace(body, `"required": ["domain"]`, `"required": ["domain"], "x-edited": true`, 1)
	handler := hostfixtures.LogHandler(t, config)

	response := hostfixtures.MissionRequest(t.Context(), handler, http.MethodPut, path, body, true)

	updated := decodeResponse[models.MissionResponse](t, response)
	if updated.Name != mission.Name || updated.Objectives[0] != "Investigate the updated objectives." || updated.RequiredActions[0] != "inspect-updated-domain" || updated.ActionDefinitions[0].Description != "Inspect the domain and retain detailed evidence." {
		t.Fatal("update did not replace objectives, actions and action details")
	}
	for _, lexical := range []string{"9007199254740993", "1.2300e-6", `\u0061`, `"x-edited":true`} {
		if !strings.Contains(response.Body, lexical) {
			t.Fatalf("update lost schema JSON %q", lexical)
		}
	}
	assertSecurityHeaders(t, response, false)
	saved := decodeResponse[models.MissionResponse](t, hostfixtures.LogRequest(t.Context(), handler, path, true))
	if !reflect.DeepEqual(saved, updated) {
		t.Fatal("mission lookup did not retain the update")
	}
	retained, err := config.OperationStore.Get(t.Context(), operation.ID)
	if err != nil || !reflect.DeepEqual(retained.Mission, operation.Mission) {
		t.Fatal("editing the mission changed an existing operation snapshot")
	}
}

func TestUpdateMissionRequiresExistingNameAndValidDefinition(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	mission, err := missions.LoadMission([]byte(hostfixtures.MissionDefinition))
	if err != nil {
		t.Fatal(err)
	}
	if err := config.MissionStore.Create(t.Context(), mission); err != nil {
		t.Fatal(err)
	}
	mission, err = config.MissionStore.Get(t.Context(), mission.Name)
	if err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	path := "/api/v1/missions/" + url.PathEscape(mission.Name)
	for _, scenario := range []struct {
		name, path, body, code string
		status                 int
	}{
		{"missing target", "/api/v1/missions/missing", strings.Replace(hostfixtures.MissionDefinition, mission.Name, "missing", 1), "mission_not_found", http.StatusNotFound},
		{"renaming", path, strings.Replace(hostfixtures.MissionDefinition, mission.Name, "renamed", 1), "invalid_request", http.StatusBadRequest},
		{"empty objectives", path, strings.Replace(hostfixtures.MissionDefinition, `["Review an action that has not been implemented yet.", "Preserve ordered guidance for later execution."]`, `[]`, 1), "invalid_request", http.StatusBadRequest},
		{"unknown field", path, strings.Replace(hostfixtures.MissionDefinition, `"objectives":`, `"unknown": true, "objectives":`, 1), "invalid_request", http.StatusBadRequest},
		{"invalid action membership", path, strings.Replace(hostfixtures.MissionDefinition, `"starting_actions": ["inspect-domain"]`, `"starting_actions": ["missing"]`, 1), "invalid_request", http.StatusBadRequest},
		{"query parameter", path + "?overwrite=true", hostfixtures.MissionDefinition, "invalid_request", http.StatusBadRequest},
		{"oversized body", path, strings.Replace(hostfixtures.MissionDefinition, "Review an action that has not been implemented yet.", strings.Repeat("x", 1<<20), 1), "request_too_large", http.StatusRequestEntityTooLarge},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			response := hostfixtures.MissionRequest(t.Context(), handler, http.MethodPut, scenario.path, scenario.body, true)

			assertAPIError(t, response, scenario.status, scenario.code)
		})
	}
	for _, authenticate := range []bool{false, true} {
		configure := func(request *http.Request) { request.Header.Del("Content-Type") }
		status, code := http.StatusUnauthorized, "authentication_required"
		if authenticate {
			status, code = http.StatusUnsupportedMediaType, "unsupported_media_type"
		}

		response := hostfixtures.MissionRequest(t.Context(), handler, http.MethodPut, path, hostfixtures.MissionDefinition, authenticate, configure)

		assertAPIError(t, response, status, code)
	}
	saved, err := config.MissionStore.Get(t.Context(), mission.Name)
	if err != nil || !reflect.DeepEqual(saved, mission) {
		t.Fatal("rejected update modified the original mission")
	}
	all, err := config.MissionStore.List(t.Context())
	if err != nil || len(all) != 1 {
		t.Fatal("rejected update created another mission")
	}
}

func TestUpdateMissionStoreFailuresAreSanitizedAndNeverRetried(t *testing.T) {
	for _, scenario := range []struct {
		code   string
		err    error
		status int
	}{
		{"request_canceled", context.Canceled, http.StatusServiceUnavailable},
		{"request_timeout", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"internal_error", errors.New("private database credentials and path"), http.StatusInternalServerError},
	} {
		t.Run(scenario.code, func(t *testing.T) {
			config := hostfixtures.LogAPIConfig(t)
			mission, err := missions.LoadMission([]byte(hostfixtures.MissionDefinition))
			if err != nil {
				t.Fatal(err)
			}
			if err := config.MissionStore.Create(t.Context(), mission); err != nil {
				t.Fatal(err)
			}
			store := fakes.NewFailedMissionUpdate(config.MissionStore, scenario.err)
			config.MissionStore = store
			handler := hostfixtures.LogHandler(t, config)

			response := hostfixtures.MissionRequest(t.Context(), handler, http.MethodPut, "/api/v1/missions/"+url.PathEscape(mission.Name), hostfixtures.MissionDefinition, true)

			assertAPIError(t, response, scenario.status, scenario.code)
			calls := store.Snapshot()
			if calls.Attempts != 1 || calls.Remaining <= 0 || calls.Remaining > 10*time.Second {
				t.Fatal("mission update was retried or lost its bounded request context")
			}
		})
	}
}
