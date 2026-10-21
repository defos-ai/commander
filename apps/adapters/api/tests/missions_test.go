package api_test

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

func TestMissionCatalogAndRepeatedCleanup(t *testing.T) {
	a := connect(t)
	catalog := read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions")
	if !slices.Equal(missionNames(catalog.Items), expectedMissions) {
		t.Fatalf("unexpected stack missions: %+v", catalog)
	}
	build := read[models.MissionResponse](a, "/api/v1/missions/build-agent")
	buildIndex := slices.IndexFunc(catalog.Items, func(item models.MissionResponse) bool { return item.Name == build.Name })
	if buildIndex < 0 || !reflect.DeepEqual(catalog.Items[buildIndex], build) || !slices.Equal(build.RequiredActions, []string{"checkout-template", "build-agent"}) || !slices.Equal(build.OptionalActions, []string{"publish-agent", "release-agent"}) {
		t.Fatalf("build mission lost its optional publication action: %+v", build)
	}
	assertSchema(t, a, "Mission", a.request(http.MethodGet, "/api/v1/missions/build-agent", nil))
	created := a.request(http.MethodPost, "/api/v1/operations", object{"mission_name": "build-agent", "input": object{
		"instructions": "Create a fixture agent",
		"actions":      []object{{"name": "inspect", "description": "Inspect supplied input.", "input_schema": object{"type": "object"}, "output_schema": object{"type": "object"}}},
	}})
	buildOperation := decode[models.OperationResponse](t, created, http.StatusCreated)
	if buildOperation.Preflight == nil || len(buildOperation.Preflight.Actions) != 2 {
		t.Fatalf("build preflight included optional publication: %+v", buildOperation.Preflight)
	}
	for _, action := range buildOperation.Preflight.Actions {
		if !slices.Contains(build.RequiredActions, action.ActionName) {
			t.Fatalf("unscheduled optional action blocked build preflight: %+v", action)
		}
	}
	decode[models.OperationResponse](t, a.request(http.MethodPost, operationPath(buildOperation.ID)+"/cancel", nil), http.StatusOK)
	for _, expected := range []struct {
		name, discovery string
		checks          []string
	}{
		{"review-website-no-browser", "discover-pages-no-browser", []string{"check-links", "check-seo", "detect-technologies"}},
		{"review-website-with-browser", "discover-pages", []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"}},
	} {
		path := "/api/v1/missions/" + expected.name
		mission := read[models.MissionResponse](a, path)
		index := slices.IndexFunc(catalog.Items, func(item models.MissionResponse) bool { return item.Name == expected.name })
		aggregate := mission.Rules["aggregate-results"]
		decision := mission.Rules["decide-next-actions"]
		if !reflect.DeepEqual(catalog.Items[index], mission) || mission.Objective == "" || len(mission.RequiredActions) != 1+len(expected.checks) || !reflect.DeepEqual(mission.StartingActions, []string{expected.discovery}) {
			t.Fatalf("default mission catalog lost schemas or actions: %+v", mission)
		}
		if len(mission.Rules) != 2 || !reflect.DeepEqual(aggregate.ForActions, []string{expected.discovery}) || aggregate.Params != nil || !reflect.DeepEqual(decision.ForActions, expected.checks) || decision.Params == nil || decision.Params.DecisionBy != "model" {
			t.Fatalf("default mission catalog lost grouping or model rules: %+v", mission.Rules)
		}
		assertSchema(t, a, "Mission", a.request(http.MethodGet, path, nil))
	}
	assertError(t, a.request(http.MethodGet, "/api/v1/missions/missing", nil), http.StatusNotFound, "mission_not_found")
	for range 2 {
		clean := a.request(http.MethodPost, "/api/v1/testing/cleanup", nil)
		if clean.status != http.StatusNoContent || clean.body != "" {
			t.Fatalf("idempotent cleanup: %+v", clean)
		}
		if current := read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions"); !reflect.DeepEqual(current, catalog) {
			t.Fatal("cleanup deleted or changed the imported missions")
		}
	}
}
