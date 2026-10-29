package api_test

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
)

func TestMissionCatalogAndRepeatedCleanup(t *testing.T) {
	a := fixtures.Connect(t)
	catalog := fixtures.Read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions")
	if !fixtures.HasExpectedMissions(catalog.Items) {
		t.Fatalf("unexpected stack missions: %+v", catalog)
	}
	build := fixtures.Read[models.MissionResponse](a, "/api/v1/missions/build-agent")
	buildIndex := slices.IndexFunc(catalog.Items, func(item models.MissionResponse) bool { return item.Name == build.Name })
	if buildIndex < 0 || !reflect.DeepEqual(catalog.Items[buildIndex], build) || !slices.Equal(build.RequiredActions, []string{"checkout-template", "build-agent"}) || !slices.Equal(build.OptionalActions, []string{"publish-agent", "release-agent"}) {
		t.Fatalf("build mission lost its optional publication action: %+v", build)
	}
	assertSchema(t, a, "Mission", a.Request(http.MethodGet, "/api/v1/missions/build-agent", nil))
	created := a.Request(http.MethodPost, "/api/v1/operations", fixtures.Object{"mission_name": "build-agent", "input": fixtures.Object{
		"instructions": "Create a fixture agent",
		"actions":      []fixtures.Object{{"name": "inspect", "description": "Inspect supplied input.", "input_schema": fixtures.Object{"type": "object"}, "output_schema": fixtures.Object{"type": "object"}}},
	}})
	buildOperation := fixtures.Decode[models.OperationResponse](t, created, http.StatusCreated)
	if buildOperation.Preflight == nil || len(buildOperation.Preflight.Actions) != 2 {
		t.Fatalf("build preflight included optional publication: %+v", buildOperation.Preflight)
	}
	for _, action := range buildOperation.Preflight.Actions {
		if !slices.Contains(build.RequiredActions, action.ActionName) {
			t.Fatalf("unscheduled optional action blocked build preflight: %+v", action)
		}
	}
	fixtures.Decode[models.OperationResponse](t, a.Request(http.MethodPost, fixtures.OperationPath(buildOperation.ID)+"/cancel", nil), http.StatusOK)
	path := "/api/v1/missions/analyze-text"
	mission := fixtures.Read[models.MissionResponse](a, path)
	index := slices.IndexFunc(catalog.Items, func(item models.MissionResponse) bool { return item.Name == mission.Name })
	aggregate := mission.Rules["aggregate-results"]
	decision := mission.Rules["decide-next-actions"]
	if index < 0 || !reflect.DeepEqual(catalog.Items[index], mission) || len(mission.Objectives) == 0 || !slices.Equal(mission.RequiredActions, []string{"analyze-text", "write-summary"}) || !slices.Equal(mission.StartingActions, []string{"analyze-text"}) {
		t.Fatalf("mission catalog lost schemas or actions: %+v", mission)
	}
	if len(mission.Rules) != 2 || !slices.Equal(aggregate.ForActions, []string{"analyze-text"}) || aggregate.Params != nil || !slices.Equal(decision.ForActions, []string{"write-summary"}) || decision.Params == nil || decision.Params.DecisionBy != "model" {
		t.Fatalf("mission catalog lost grouping or model rules: %+v", mission.Rules)
	}
	assertSchema(t, a, "Mission", a.Request(http.MethodGet, path, nil))
	fixtures.AssertError(t, a.Request(http.MethodGet, "/api/v1/missions/missing", nil), http.StatusNotFound, "mission_not_found")
	for range 2 {
		clean := a.Request(http.MethodPost, "/api/v1/testing/cleanup", nil)
		if clean.Status != http.StatusNoContent || clean.Body != "" {
			t.Fatalf("idempotent cleanup: %+v", clean)
		}
		if current := fixtures.Read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions"); !reflect.DeepEqual(current, catalog) {
			t.Fatal("cleanup deleted or changed the imported missions")
		}
	}
}
