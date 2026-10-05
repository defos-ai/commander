package api_test

import (
	"net/http"
	"reflect"
	"testing"

	"commander/adapters/api/models"
)

func TestMissionCatalogAndRepeatedCleanup(t *testing.T) {
	a := connect(t)
	catalog := read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions")
	mission := read[models.MissionResponse](a, "/api/v1/missions/review-website")
	if len(catalog.Items) != 1 || !reflect.DeepEqual(catalog.Items[0], mission) || mission.Objective == "" || len(mission.RequiredActions) != 6 || !reflect.DeepEqual(mission.StartingActions, []string{"discover-pages"}) || len(mission.Automations["discover-pages"].Run) != 5 {
		t.Fatalf("default mission catalog lost schemas or automation: %+v", mission)
	}
	assertSchema(t, a, "Mission", a.request(http.MethodGet, "/api/v1/missions/review-website", nil))
	assertError(t, a.request(http.MethodGet, "/api/v1/missions/missing", nil), http.StatusNotFound, "mission_not_found")
	for range 2 {
		clean := a.request(http.MethodPost, "/api/v1/testing/cleanup", nil)
		if clean.status != http.StatusNoContent || clean.body != "" {
			t.Fatalf("idempotent cleanup: %+v", clean)
		}
		if current := read[models.MissionResponse](a, "/api/v1/missions/review-website"); !reflect.DeepEqual(current, mission) {
			t.Fatal("cleanup deleted or changed the imported mission")
		}
	}
}
