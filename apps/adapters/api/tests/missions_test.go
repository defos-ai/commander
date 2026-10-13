package api_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

func TestMissionCatalogAndRepeatedCleanup(t *testing.T) {
	a := connect(t)
	catalog := read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions")
	if len(catalog.Items) != 2 {
		t.Fatalf("unexpected default missions: %+v", catalog)
	}
	for i, expected := range []struct {
		name, discovery string
		checks          []string
	}{
		{"review-website-no-browser", "discover-pages-no-browser", []string{"check-links", "check-seo", "detect-technologies"}},
		{"review-website-with-browser", "discover-pages", []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"}},
	} {
		path := "/api/v1/missions/" + expected.name
		mission := read[models.MissionResponse](a, path)
		aggregate := mission.Rules["aggregate-results"]
		decision := mission.Rules["decide-next-actions"]
		if !reflect.DeepEqual(catalog.Items[i], mission) || mission.Objective == "" || len(mission.RequiredActions) != 1+len(expected.checks) || !reflect.DeepEqual(mission.StartingActions, []string{expected.discovery}) {
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
